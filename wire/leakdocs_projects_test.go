package wire

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/leakcheck"
)

// TestLeakProjectFolder: which project folder a repo-relative path names.
func TestLeakProjectFolder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path, folder string
		ok           bool
	}{
		{".lore/data/tasks/tsk_1.json", "", true},
		{"web/.lore/data/tasks/tsk_1.json", "web", true},
		{"services/billing/.lore/data/_meta.json", "services/billing", true},
		{"web/src/main.go", "", false},
		{"commit message", "", false},
		{"web.lore/data/tasks/tsk_1.json", "", false}, // not a .lore folder
		{"x/.lore/state/audit-v2.jsonl", "", false},
	}
	for _, c := range cases {
		f, ok := leakProjectFolder(c.path)
		if f != c.folder || ok != c.ok {
			t.Errorf("%s: (%q, %v), want (%q, %v)", c.path, f, ok, c.folder, c.ok)
		}
	}
}

// TestGroupLeakItems: each row file is checked against the project of the
// folder holding it (OWNERQ-1); a folder with no project of its own, and
// every doc that is not a row file, stays with the command's project (fail
// closed: the rule keeps applying as before).
func TestGroupLeakItems(t *testing.T) {
	t.Parallel()
	projects := map[string]string{"": "prj_top", "web": "prj_web", "services/billing": "prj_bill", "broken": ""}
	projectOf := func(f string) (string, bool) {
		p, ok := projects[f]
		return p, ok
	}
	paths := []string{
		"commit message",
		".lore/data/tasks/tsk_a.json",
		"web/.lore/data/tasks/tsk_b.json",
		"web/src/x.go",
		"services/billing/.lore/data/tasks/tsk_c.json",
		"docs/.lore/data/tasks/tsk_d.json",   // no project there
		"broken/.lore/data/tasks/tsk_e.json", // a _meta.json with no project id
	}
	items := make([]leakcheck.Item, len(paths))
	for i, p := range paths {
		items[i] = leakcheck.Item{Path: p}
	}
	cases := []struct {
		name, own, ownPID string
		want              map[string][]string // project id -> paths
	}{
		{"command at the repo top", "", "prj_top", map[string][]string{
			"prj_top":  {"commit message", ".lore/data/tasks/tsk_a.json", "web/src/x.go", "docs/.lore/data/tasks/tsk_d.json", "broken/.lore/data/tasks/tsk_e.json"},
			"prj_web":  {"web/.lore/data/tasks/tsk_b.json"},
			"prj_bill": {"services/billing/.lore/data/tasks/tsk_c.json"},
		}},
		{"command in a sub-project", "web", "prj_web", map[string][]string{
			"prj_web":  {"commit message", "web/.lore/data/tasks/tsk_b.json", "web/src/x.go", "docs/.lore/data/tasks/tsk_d.json", "broken/.lore/data/tasks/tsk_e.json"},
			"prj_top":  {".lore/data/tasks/tsk_a.json"},
			"prj_bill": {"services/billing/.lore/data/tasks/tsk_c.json"},
		}},
		{"nested sub-project", "services/billing", "prj_bill", map[string][]string{
			"prj_bill": {"commit message", "web/src/x.go", "services/billing/.lore/data/tasks/tsk_c.json", "docs/.lore/data/tasks/tsk_d.json", "broken/.lore/data/tasks/tsk_e.json"},
			"prj_top":  {".lore/data/tasks/tsk_a.json"},
			"prj_web":  {"web/.lore/data/tasks/tsk_b.json"},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			groups := groupLeakItems(items, c.own, c.ownPID, projectOf)
			if groups[0].projectID != c.ownPID {
				t.Fatalf("own group first: got %s", groups[0].projectID)
			}
			got := map[string][]string{}
			n := 0
			for _, g := range groups {
				if _, dup := got[g.projectID]; dup {
					t.Fatalf("project %s split into two groups", g.projectID)
				}
				for _, it := range g.items {
					got[g.projectID] = append(got[g.projectID], it.Path)
					n++
				}
			}
			if n != len(items) {
				t.Fatalf("%d of %d items grouped (none may be lost)", n, len(items))
			}
			for pid, want := range c.want {
				g := got[pid]
				slices.Sort(g)
				w := slices.Clone(want)
				slices.Sort(w)
				if !slices.Equal(g, w) {
					t.Errorf("%s: %v, want %v", pid, g, w)
				}
			}
		})
	}
	t.Run("no items keeps one own group", func(t *testing.T) {
		g := groupLeakItems(nil, "", "prj_top", projectOf)
		if len(g) != 1 || g[0].projectID != "prj_top" || len(g[0].items) != 0 {
			t.Fatalf("%+v", g)
		}
	})
}

// TestGroupLeakItemsKeepsTheRule: a foreign row filed in a sub-project's
// folder is still "row of another project" for that sub-project, and a row
// of the sub-project filed at the top still is for the top (the split
// narrows nothing).
func TestGroupLeakItemsKeepsTheRule(t *testing.T) {
	t.Parallel()
	projectOf := func(f string) (string, bool) { return map[string]string{"web": "prj_web"}[f], f == "web" }
	row := func(path, pid string) leakcheck.Item {
		return leakcheck.Item{Where: "commit " + path, Path: path, Content: []byte(`{"_v":2,"id":"tsk_01a1acc2ce5776b79cd587677332acfe","project_id":"` + pid + `"}`)}
	}
	cases := []struct {
		name string
		it   leakcheck.Item
		want bool // a "row of another project" finding
	}{
		{"web row in web", row("web/.lore/data/tasks/tsk_01a1acc2ce5776b79cd587677332acfe.json", "prj_web"), false},
		{"top row at the top", row(".lore/data/tasks/tsk_01a1acc2ce5776b79cd587677332acfe.json", "prj_top"), false},
		{"foreign row in web", row("web/.lore/data/tasks/tsk_01a1acc2ce5776b79cd587677332acfe.json", "prj_other"), true},
		{"web row at the top", row(".lore/data/tasks/tsk_01a1acc2ce5776b79cd587677332acfe.json", "prj_web"), true},
		{"top row in web", row("web/.lore/data/tasks/tsk_01a1acc2ce5776b79cd587677332acfe.json", "prj_top"), true},
		{"web row in a folder with no project", row("docs/.lore/data/tasks/tsk_01a1acc2ce5776b79cd587677332acfe.json", "prj_web"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var fs []leakcheck.Finding
			for _, g := range groupLeakItems([]leakcheck.Item{c.it}, "", "prj_top", projectOf) {
				fs = append(fs, leakcheck.Check(g.items, nil, leakcheck.Options{RepoProjectID: g.projectID})...)
			}
			got := slices.ContainsFunc(fs, func(f leakcheck.Finding) bool { return f.Detail == "row of another project" })
			if got != c.want {
				t.Fatalf("row of another project = %v, want %v (%+v)", got, c.want, fs)
			}
		})
	}
}
