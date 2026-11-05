package pattern_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
)

// TestScopeFlag (CONTEXT_BUDGET 6.2.3, CQ12, OV53) runs --scope on add and
// edit for both knowledge nouns that share this package's shape (pattern
// and architecturenote): dir:<path> binds the repo and a scp_ folder row;
// repo keeps the repo and clears the folder; project clears both; a folder
// that does not exist, an org scope, global with an edit, and --scope next
// to --rebind-repo are refused. A list without --repo or --all-repos shows
// project-wide rows only (v0.1.17's ScopeMaster default, decision
// G-RENDER-1): a repo-bound row is hidden until --all-repos.
func TestScopeFlag(t *testing.T) {
	for _, noun := range []string{"pattern", "architecturenote"} {
		t.Run(noun, func(t *testing.T) {
			h := opstest.New(t)
			meta := filepath.Join(h.Repo, rowfs.LoreDir, rowfs.DataDir)
			if err := os.MkdirAll(meta, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(meta, rowfs.MetaFileName), []byte(`{"_v":1,"project_id":"`+h.Project+`"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			rid, _, err := h.App.EnsureRepoIdentity(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(h.Repo, "src", "booking"), 0o750); err != nil {
				t.Fatal(err)
			}
			show := func(id string) (scope, repo string) {
				t.Helper()
				doc := h.JSON(h.Must(noun+".show", `{"id":"`+id+`"}`))
				return contenttest.Str(t, doc, "data", "scope_id"), contenttest.Str(t, doc, "data", "repo_id")
			}
			add := func(title, scope string) string {
				t.Helper()
				return h.ID(h.Must(noun+".add", `{"title":"`+title+`","body":"b","scope":"`+scope+`"}`))
			}

			dir := add("folder", "dir:src/booking")
			scp, repo := show(dir)
			if !strings.HasPrefix(scp, "scp_") || repo != rid {
				t.Fatalf("dir scope: scope_id %q repo_id %q", scp, repo)
			}
			// A second row on the same folder reuses the scope row.
			if scp2, _ := show(add("folder2", "dir:src/booking")); scp2 != scp {
				t.Fatalf("second row made scope %q, want %q", scp2, scp)
			}
			if s, r := show(add("repo", "repo")); s != "" || r != repo {
				t.Fatalf("repo scope: scope_id %q repo_id %q", s, r)
			}
			if s, r := show(add("plain", "project")); s != "" || r != "" {
				t.Fatalf("project scope: scope_id %q repo_id %q", s, r)
			}

			// Edit: dir -> repo clears the folder, keeps the repo; -> project clears both.
			h.Must(noun+".edit", `{"id":"`+dir+`","scope":"repo"}`)
			if s, r := show(dir); s != "" || r != repo {
				t.Fatalf("edit to repo: scope_id %q repo_id %q", s, r)
			}
			h.Must(noun+".edit", `{"id":"`+dir+`","scope":"dir:src/booking"}`)
			if s, _ := show(dir); s != scp {
				t.Fatalf("edit back to dir: scope_id %q", s)
			}
			h.Must(noun+".edit", `{"id":"`+dir+`","scope":"project"}`)
			if s, r := show(dir); s != "" || r != "" {
				t.Fatalf("edit to project: scope_id %q repo_id %q", s, r)
			}

			// Refusals.
			for _, c := range []struct{ name, in string }{
				{noun + ".add", `{"title":"x","body":"b","scope":"dir:no/such"}`},
				{noun + ".add", `{"title":"x","body":"b","scope":"org"}`},
				{noun + ".add", `{"title":"x","body":"b","scope":"repo","repo":"other"}`},
				{noun + ".edit", `{"id":"` + dir + `","scope":"global"}`},
				{noun + ".edit", `{"id":"` + dir + `","scope":"repo","rebind_master":true}`},
			} {
				h.Fails(c.name, c.in, errs.InvalidInput)
			}

			// G-RENDER-1: repo-bound rows are hidden from a plain list.
			plain := h.JSON(h.Must(noun+".list", `{}`))
			all := h.JSON(h.Must(noun+".list", `{"all_repos":true}`))
			for _, title := range []string{"folder2", "repo"} {
				if strings.Contains(plain, `"`+title+`"`) || !strings.Contains(all, `"`+title+`"`) {
					t.Fatalf("repo-bound %q: plain list %v, --all-repos %v", title, strings.Contains(plain, title), strings.Contains(all, title))
				}
			}
			if !strings.Contains(plain, `"plain"`) {
				t.Fatal("plain list lost the project-wide row")
			}
		})
	}
}
