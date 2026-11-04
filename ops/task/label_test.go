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
	// task / memory / rule / decision / hotfix lines did; the others show
	// none).
	human bool
	// addLine is the first human line of the add for the new id and the
	// store label (v0.1.17 printed route.Label there); nil = not checked.
	addLine func(id, label string) string
}

// labelCases covers each output family: task (work + its own text),
// tasklist / plan / mission (ops/internal/work), memory, rule / decision /
// hotfix (ops/internal/knowledge), pattern (ops/content) and comment-free
// content-2 (playbook).
func labelCases(to string) []labelCase {
	q := `"to":"` + to + `"`
	return []labelCase{
		{"task.add", `{"title":"labelled task",` + q + `}`, "task.list", true,
			func(id, l string) string { return "✓ " + id + " — labelled task [medium] → " + l }},
		{"tasklist.add", `{"title":"labelled list","body":"b",` + q + `}`, "tasklist.list", false, nil},
		{"plan.add", `{"title":"labelled plan","body":"steps",` + q + `}`, "plan.list", false, nil},
		{"mission.add", `{"title":"labelled mission",` + q + `}`, "mission.list", false,
			func(id, l string) string { return "✓ " + id + " — labelled mission → " + l }},
		{"memory.add", `{"body":"labelled memory",` + q + `}`, "memory.list", true,
			func(id, l string) string { return "✓ " + id + " " + l }},
		{"rule.add", `{"body":"labelled rule",` + q + `}`, "rule.list", true,
			func(id, l string) string { return "✓ " + id + " " + l }},
		{"decision.add", `{"title":"labelled decision","body":"why",` + q + `}`, "decision.list", true,
			func(id, l string) string { return "✓ " + id + " — labelled decision " + l }},
		{"hotfix.add", `{"title":"labelled hotfix","body":"fix",` + q + `}`, "hotfix.list", true, nil},
		{"pattern.add", `{"title":"labelled pattern","body":"p",` + q + `}`, "pattern.list", false, nil},
		{"playbook.add", `{"title":"labelled playbook","body":"p",` + q + `}`, "playbook.list", false, nil},
	}
}

// TestStoreLabelInOutputs: a row of a store attached at a linked folder
// carries v0.1.17's `_label` "[northwind/acme]" next to `_source`, the
// FLAG1 object {store, store_id, folder, folder_id} (names plus permanent
// sto_/prj_ ids; store and folder read back as `--to northwind/acme`), and
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
		name, label, folder string
		h                   *opstest.H
	}{
		{"folder-attached", "[northwind/acme]", "acme", opstest.New(t, folderAttached)},
		{"root-attached", "[" + orgStore + "]", "", opstest.New(t, withOrg(t), repoClient(false))},
	}
	for _, s := range setups {
		to := "northwind"
		if s.name == "root-attached" {
			to = orgStore
		}
		for _, c := range labelCases(to) {
			t.Run(s.name+"/"+c.list, func(t *testing.T) {
				added, err := s.h.RunYes(c.add, c.input)
				if err != nil {
					t.Fatalf("%s %s: %v", c.add, c.input, err)
				}
				if c.addLine != nil {
					first, _, _ := strings.Cut(opstest.Text(added), "\n")
					if want := c.addLine(s.h.ID(added), s.label); first != want {
						t.Errorf("%s line\n got %q\nwant %q", c.add, first, want)
					}
				}
				res := s.h.Must(c.list, `{}`)
				js := s.h.JSON(res)
				want := sourceRE(to, s.folder, s.label)
				if !want.MatchString(js) {
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
		if !sourceRE("northwind", "acme", "[northwind/acme]").MatchString(js) {
			t.Errorf("%s hit lacks the label: %s", op, js)
		}
	}
}

// sourceRE matches the FLAG1 `_source` object of a row of store (attached
// at folder; "" = its root, which has no folder keys) followed by its
// `_label`: the names plus the permanent sto_ and prj_ ids.
func sourceRE(store, folder, label string) *regexp.Regexp {
	obj := `"_source":\{"store":"` + regexp.QuoteMeta(store) + `","store_id":"sto_[0-9a-f]{32}"`
	if folder != "" {
		obj += `,"folder":"` + regexp.QuoteMeta(folder) + `","folder_id":"prj_[0-9a-f]{32}"`
	}
	return regexp.MustCompile(obj + `\},"_label":"` + regexp.QuoteMeta(label) + `"`)
}

// TestExternalRowLines: memory and rule lists print the repo's rows first in
// their own form, then each row of another store as v0.1.17's external row
// "<id>  <text>  <label>" (stores_reads.go printExternalRows: text folded
// onto one line, never cut); the add lines of memory, rule and comment end
// in the label, and in the repo in v0.1.17's trailing space.
func TestExternalRowLines(t *testing.T) {
	h0 := opstest.WithStores(t, []string{"northwind"})
	b, err := os.ReadFile(filepath.Join(h0.Repo, ".lore", "stores.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	url := regexp.MustCompile(`url: (\S+)`).FindStringSubmatch(string(b))[1]
	h := opstest.New(t, func(o *wire.Options) {
		o.OrgStores = func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: "northwind", URL: url, Folder: "acme"}}, nil
		}
		o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
	})
	const lab = "[northwind/acme]"
	long := strings.Repeat("w ", 60) // over v0.1.17's 80-byte repo cut: external rows are not cut
	ext := h.ID(h.MustYes("memory.add", `{"body":"multi\n  line\tbody `+long+`","to":"northwind"}`))
	repoAdd := h.MustYes("memory.add", `{"body":"repo memory"}`)
	repo := h.ID(repoAdd)
	if got, want := opstest.Text(repoAdd), "✓ "+repo+" \n  scope: [master]\n"; got != want {
		t.Errorf("repo memory add %q, want %q", got, want)
	}
	wantList := repo + " [retrieved] repo memory\n" +
		ext + "  multi line body " + strings.TrimSpace(long) + "  " + lab + "\n"
	if got := opstest.Text(h.Must("memory.list", `{}`)); got != wantList {
		t.Errorf("memory list\n got %q\nwant %q", got, wantList)
	}
	rext := h.ID(h.MustYes("rule.add", `{"body":"store rule","to":"northwind"}`))
	rrepo := h.ID(h.MustYes("rule.add", `{"body":"repo rule"}`))
	rl := opstest.Text(h.Must("rule.list", `{}`))
	if !strings.HasSuffix(rl, rext+"  store rule  "+lab+"\n") || !strings.HasPrefix(rl, rrepo+" [") {
		t.Errorf("rule list: repo rows first, then %q:\n%s", rext+"  store rule  "+lab, rl)
	}
	c := opstest.Text(h.MustYes("comment.add", `{"on_table":"memories","on_id":"`+ext+`","body":"hi","to":"northwind"}`))
	if !strings.HasSuffix(c, " on memories/"+ext+" "+lab+"\n") {
		t.Errorf("store comment add %q", c)
	}
}
