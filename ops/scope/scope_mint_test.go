package scope_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/wire"
)

// TestScopedWriteMintsRepoIdentity pins devco:M-4: in a migrated clone
// whose _meta.json has no repo_id (lore 0.1.17 data had no repos row, and
// only a sync with rows waiting for a commit mints one), a dir-scoped rule
// or decision was refused with "no repo identity yet / run `lore init`",
// and lore init answered "nothing to do". The scoped write now mints the
// derived id every clone agrees on, writes the repos row beside the rule,
// and a second write reuses it.
func TestScopedWriteMintsRepoIdentity(t *testing.T) {
	// an own repo, as cmd/lore decides it (no hook fails closed to client)
	h := opstest.New(t, func(o *wire.Options) { o.RepoIsClient = func(context.Context) (bool, error) { return false, nil } })
	meta := filepath.Join(h.Repo, rowfs.LoreDir, rowfs.DataDir)
	if err := os.MkdirAll(filepath.Join(h.Repo, "packages", "dock"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(meta, 0o750); err != nil {
		t.Fatal(err)
	}
	// a migrated project's _meta.json: format 2, a project, no repo_id
	if err := os.WriteFile(filepath.Join(meta, rowfs.MetaFileName), []byte(`{"_v":1,"format":2,"project_id":"`+h.Project+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.App.RepoIdentity(context.Background()); ok {
		t.Fatal("the fixture already has a repo identity")
	}
	h.Must("rule.add", `{"body":"Dock popouts restore focus to the opener.","summary":"Dock restores focus","severity":"must","scope":"dir:packages/dock"}`)
	rid, ok := h.App.RepoIdentity(context.Background())
	if !ok || !strings.HasPrefix(rid, "rep_") {
		t.Fatalf("after a dir-scoped rule the checkout has no repo identity (%q, %v)", rid, ok)
	}
	if _, err := os.Stat(filepath.Join(meta, "repos", rid+".json")); err != nil {
		t.Fatalf("the repos row of %s was not written: %v", rid, err)
	}
	h.Must("decision.add", `{"title":"Dock focus","body":"restore it","scope":"dir:packages/dock"}`)
	if again, _ := h.App.RepoIdentity(context.Background()); again != rid {
		t.Fatalf("the second scoped write changed the repo id: %s → %s", rid, again)
	}
}
