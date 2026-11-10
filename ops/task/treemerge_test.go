package task_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// setRank writes rank into the task's row file, as a merge does.
func setRank(h *opstest.H, id, rank string) {
	h.T.Helper()
	root, err := os.OpenRoot(filepath.Join(h.Repo, ".lore", "data", schema.TableTasks))
	if err != nil {
		h.T.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	b, err := root.ReadFile(id + ".json")
	if err != nil {
		h.T.Fatal(err)
	}
	doc := rankRe.ReplaceAllString(string(b), "")
	doc = strings.Replace(doc, "{", `{"`+schema.FieldTaskRank+`":"`+rank+`",`, 1)
	if err := root.WriteFile(id+".json", []byte(doc), 0o600); err != nil {
		h.T.Fatal(err)
	}
}

// rankRe matches a stored rank member.
var rankRe = regexp.MustCompile(`"` + schema.FieldTaskRank + `":\s*"[^"]*",?`)

// treeView is what one machine shows of the merged rows: the tree check's
// findings (JSON) and the top-level and per-parent listings.
type treeView struct {
	check string
	top   []string
	kids  map[string][]string
}

// viewOf reads the tree as h shows it.
func viewOf(h *opstest.H, parents ...string) treeView {
	h.T.Helper()
	listIDs := func(in string) []string {
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpList, in))), &out); err != nil {
			h.T.Fatal(err)
		}
		var got []string
		for _, d := range out.Data {
			got = append(got, d.ID)
		}
		return got
	}
	v := treeView{check: h.JSON(h.Must(opstask.OpTreeCheck, `{}`)), top: listIDs(`{"top":true,"all":true}`), kids: map[string][]string{}}
	slices.Sort(v.top)
	for _, p := range parents {
		v.kids[p] = listIDs(`{"parent":"` + p + `","all":true}`)
	}
	return v
}

// TestTreeMergeMatrix (TASK 6.13 / 6.15 at the ops level): every merged
// combination that is valid per file but odd as a tree is shown the same
// way by two independent imports of the same row files (the writing App,
// and a cold reopen with no cache, as a teammate's clone reads it): a
// three-task cycle shows its byte-wise smallest id at top level, a shared
// rank orders by id and is reported as info, a subtree pushed over
// tasks.max_depth is kept and reported, and nothing is repaired on read.
func TestTreeMergeMatrix(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpPolicySet, `{"key":"tasks.max_depth","value":"2"}`)

	// A three-task cycle (two machines each re-parented one task).
	cyc := []string{add(h, "c1", ""), add(h, "c2", ""), add(h, "c3", "")}
	setParent(h, cyc[0], cyc[1])
	setParent(h, cyc[1], cyc[2])
	setParent(h, cyc[2], cyc[0])
	smallest := slices.Min(cyc)

	// Two siblings with one rank: ordered by id.
	p := add(h, "parent", "")
	s1 := add(h, "sib one", `"parent":"`+p+`"`)
	s2 := add(h, "sib two", `"parent":"`+p+`"`)
	setRank(h, s1, "a0")
	setRank(h, s2, "a0")

	// A chain of depth 3 under max_depth 2 (a merge joined two subtrees).
	d0 := add(h, "d0", "")
	d1 := add(h, "d1", `"parent":"`+d0+`"`)
	d2 := add(h, "d2", "")
	d3 := add(h, "d3", `"parent":"`+d2+`"`)
	setParent(h, d2, d1)

	dir, err := os.OpenRoot(filepath.Join(h.Repo, ".lore", "data", schema.TableTasks))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	before := map[string][]byte{}
	for _, id := range append(slices.Clone(cyc), s1, s2, d2, d3) {
		b, rerr := dir.ReadFile(id + ".json")
		if rerr != nil {
			t.Fatal(rerr)
		}
		before[id] = b
	}

	a := viewOf(h, p, smallest)
	b := viewOf(h.ReopenCold(), p, smallest)
	if a.check != b.check {
		t.Errorf("tree check differs between machines:\n%s\n%s", a.check, b.check)
	}
	if !slices.Equal(a.top, b.top) {
		t.Errorf("top level differs: %v vs %v", a.top, b.top)
	}
	for k := range a.kids {
		if !slices.Equal(a.kids[k], b.kids[k]) {
			t.Errorf("children of %s differ: %v vs %v", k, a.kids[k], b.kids[k])
		}
	}

	// The cycle: only the smallest id is top level.
	for _, id := range cyc {
		if slices.Contains(a.top, id) != (id == smallest) {
			t.Errorf("cycle member %s top=%v (smallest %s)", id, slices.Contains(a.top, id), smallest)
		}
	}
	// The shared rank: id order, and an info finding.
	want := []string{s1, s2}
	slices.Sort(want)
	if !slices.Equal(a.kids[p], want) {
		t.Errorf("shared rank order %v want %v", a.kids[p], want)
	}
	for _, w := range []string{`"kind":"parent_cycle"`, `"kind":"shared_rank"`, `"kind":"over_depth"`, `"id":"` + d3 + `"`} {
		if !strings.Contains(a.check, w) {
			t.Errorf("tree check lacks %s: %s", w, a.check)
		}
	}
	// Reading never repairs: the row files are byte-equal afterwards.
	for id, old := range before {
		now, err := dir.ReadFile(id + ".json")
		if err != nil {
			t.Fatal(err)
		}
		if string(now) != string(old) {
			t.Errorf("%s rewritten by a read", id)
		}
	}
}
