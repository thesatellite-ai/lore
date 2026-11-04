package task_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/wire"
)

// labelCase is one noun's write-then-read walk: the add op and its input
// (written into the store named by to), and the list op whose --json and
// human text must carry the store label.
type labelCase struct {
	add, input, list string
	// human: the noun's human list line shows the store label (v0.1.17's
	// task / rule / decision / hotfix lines did; the others show none).
	human bool
}

// labelCases covers each output family: task (work + its own text),
// tasklist / plan / mission (ops/internal/work), memory, rule / decision /
// hotfix (ops/internal/knowledge), pattern (ops/content) and comment-free
// content-2 (playbook).
func labelCases(to string) []labelCase {
	q := `"to":"` + to + `"`
	return []labelCase{
		{"task.add", `{"title":"labelled task",` + q + `}`, "task.list", true},
		{"tasklist.add", `{"title":"labelled list","body":"b",` + q + `}`, "tasklist.list", false},
		{"plan.add", `{"title":"labelled plan","body":"steps",` + q + `}`, "plan.list", false},
		{"mission.add", `{"title":"labelled mission",` + q + `}`, "mission.list", false},
		{"memory.add", `{"body":"labelled memory",` + q + `}`, "memory.list", false},
		{"rule.add", `{"body":"labelled rule",` + q + `}`, "rule.list", true},
		{"decision.add", `{"title":"labelled decision","body":"why",` + q + `}`, "decision.list", true},
		{"hotfix.add", `{"title":"labelled hotfix","body":"fix",` + q + `}`, "hotfix.list", true},
		{"pattern.add", `{"title":"labelled pattern","body":"p",` + q + `}`, "pattern.list", false},
		{"playbook.add", `{"title":"labelled playbook","body":"p",` + q + `}`, "playbook.list", false},
	}
}

// TestStoreLabelInOutputs: a row of a store attached at a linked folder
// carries v0.1.17's `_label` "[northwind/acme]" next to `_source`
// "northwind" (the store NAME, unchanged so --to/--store round-trip), and
// the human list lines that show a store print the label; a store
// attached at its root is labelled with its name in both. v0.1.17
// (stores_reads.go, stores_cache.go) set `_label` to the cache's Label,
// "[" + org + "/" + folder + "]", and printed the same text at the end of
// an external row's human line.
func TestStoreLabelInOutputs(t *testing.T) {
	h0 := opstest.WithStores(t, []string{"northwind"})
	b, err := os.ReadFile(filepath.Join(h0.Repo, ".lore", "stores.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	url := regexp.MustCompile(`url: (\S+)`).FindStringSubmatch(string(b))[1]
	folderAttached := func(o *wire.Options) {
		o.OrgStores = func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: "northwind", URL: url, Folder: "acme"}}, nil
		}
		o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
	}
	setups := []struct {
		name, label string
		h           *opstest.H
	}{
		{"folder-attached", "[northwind/acme]", opstest.New(t, folderAttached)},
		{"root-attached", "[" + orgStore + "]", opstest.New(t, withOrg(t), repoClient(false))},
	}
	for _, s := range setups {
		to := "northwind"
		if s.name == "root-attached" {
			to = orgStore
		}
		for _, c := range labelCases(to) {
			t.Run(s.name+"/"+c.list, func(t *testing.T) {
				if _, err := s.h.RunYes(c.add, c.input); err != nil {
					t.Fatalf("%s %s: %v", c.add, c.input, err)
				}
				res := s.h.Must(c.list, `{}`)
				js := s.h.JSON(res)
				want := `"_source":"` + to + `","_label":"` + s.label + `"`
				if !strings.Contains(js, want) {
					t.Errorf("%s --json lacks %s:\n%s", c.list, want, js)
				}
				if txt := opstest.Text(res); c.human && !strings.Contains(txt, s.label) {
					t.Errorf("%s human lacks %s:\n%s", c.list, s.label, txt)
				}
			})
		}
	}
	// A repo row has neither key.
	h := setups[1].h
	if _, err := h.RunYes("memory.add", `{"body":"repo memory"}`); err != nil {
		t.Fatal(err)
	}
	js := h.JSON(h.Must("memory.search", `{"query":"repo"}`))
	if strings.Contains(js, `"_label"`) || strings.Contains(js, `"_source"`) {
		t.Errorf("repo hit must carry no _source/_label: %s", js)
	}
	// Search hits carry the label too (memory, knowledge, content, work).
	f := setups[0].h
	for _, op := range []string{"memory.search", "rule.search", "pattern.search", "task.search"} {
		js := f.JSON(f.Must(op, `{"query":"labelled"}`))
		if !strings.Contains(js, `"_source":"northwind","_label":"[northwind/acme]"`) {
			t.Errorf("%s hit lacks the label: %s", op, js)
		}
	}
}
