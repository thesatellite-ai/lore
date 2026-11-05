package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/store"
)

// trailerTap records the commit trailers each Write of one store carries
// (what a git backend puts into that commit).
type trailerTap struct {
	store.Backend
	mu  *sync.Mutex
	got *[]string
}

func (t trailerTap) Write(ctx context.Context, changes []store.Change) error {
	t.mu.Lock()
	*t.got = append(*t.got, store.CommitTrailers(ctx)...)
	t.mu.Unlock()
	return t.Backend.Write(ctx, changes)
}

func (t trailerTap) Unwrap() store.Backend { return t.Backend }

// TestMoveIntoClientRepoTrailer (OV44, PROVENANCE 5.4): a memory moved
// from an org store into a client repo leaves a tombstone commit in the
// org store whose Lore-Moved-To trailer names the client repo by its
// permanent rep_ id (the client file never names the hop, so our side's
// history is stitched by that id), never by the local name "repo".
func TestMoveIntoClientRepoTrailer(t *testing.T) {
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	var (
		mu  sync.Mutex
		got []string
	)
	repo := gitRepo(t)
	cfg := filepath.Join(repo, ".git", "config")
	f, err := os.OpenFile(filepath.Clean(cfg), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("[remote \"origin\"]\n\turl = git@github.com:acme/client-app.git\n"); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	a := openWith(t, repo, Options{
		OrgStores: func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: "agency", URL: orgURL, Folder: "acme"}}, nil
		},
		RepoIsClient: func(context.Context) (bool, error) { return true, nil },
		WrapBackend: func(_ context.Context, name store.Source, _ string, b store.Backend) store.Backend {
			if name != "agency" {
				return b
			}
			return trailerTap{Backend: b, mu: &mu, got: &got}
		},
	})
	withProject(t, a)
	id := idOf(t, mustRun(t, a, opsmemory.OpAdd, `{"body":"for the client","to":"agency"}`))
	mu.Lock()
	got = nil
	mu.Unlock()
	if _, err := run(t, a, opsmemory.OpMove, `{"id":"`+id+`","to":"repo"}`, true); err != nil {
		t.Fatalf("move into the client repo: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	var moved []string
	for _, tr := range got {
		if strings.HasPrefix(tr, engine.MovedToTrailer+":") {
			moved = append(moved, tr)
		}
	}
	if len(moved) != 1 {
		t.Fatalf("org store commits carried %v, want exactly one %s", got, engine.MovedToTrailer)
	}
	if v := strings.TrimSpace(strings.TrimPrefix(moved[0], engine.MovedToTrailer+":")); !strings.HasPrefix(v, "rep_") {
		t.Fatalf("%s names %q, want the client repo's rep_ id", engine.MovedToTrailer, v)
	}
}

// mustRun is run that fails the test on an error.
func mustRun(t *testing.T, a *App, name, input string) string {
	t.Helper()
	out, err := run(t, a, name, input, true)
	if err != nil {
		t.Fatalf("%s %s: %v", name, input, err)
	}
	return out
}
