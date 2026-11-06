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
	"github.com/thesatellite-ai/lore/model/ids"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
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
	t.Parallel()
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
	// PROVENANCE 5.4: the trailer names the rep_ id "when known", so the
	// checkout gets its id first (what `lore init` does in its own
	// process); the repo store's place is read when a later command opens.
	repoID := mintRepoID(t, repo)
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
	if v := strings.TrimSpace(strings.TrimPrefix(moved[0], engine.MovedToTrailer+":")); v != repoID {
		t.Fatalf("%s names %q, want the client repo's rep_ id %s", engine.MovedToTrailer, v, repoID)
	}
}

// TestRepoPlaceKeepsOriginStore: the repo store's place id is its rep_ id
// (moves, trailers), yet a row created in the repo store keeps `_o.store:
// repo`, which the v0.1.17 leak check reads (leakcheck originIsRepo); only
// a marked store's sto_ id replaces the attached name in _o (r3-origin).
func TestRepoPlaceKeepsOriginStore(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	mintRepoID(t, repo)
	a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	id := idOf(t, mustRun(t, a, opsmemory.OpAdd, `{"body":"born in the repo"}`))
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	_, got, err := eng.Get(background(), engine.Ref{Store: store.SourceRepo, Table: schema.TableMemories, ID: aid})
	if err != nil {
		t.Fatal(err)
	}
	if o := got.Origin.String(); !strings.Contains(o, `"store":"repo"`) {
		t.Fatalf("_o of a repo-store row = %s, want store repo", o)
	}
}

// mintRepoID does what `lore init` does in its own process: the project
// row, .lore/data/_meta.json and the checkout's permanent rep_ id (PROV1).
// It returns the id; an App opened afterwards reads it as the repo place.
func mintRepoID(t *testing.T, repo string) string {
	t.Helper()
	a := openWith(t, repo, Options{})
	withProject(t, a)
	pid, err := a.Project(background())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repo, rowfs.LoreDir, rowfs.DataDir)
	if err = os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, rowfs.MetaFileName), []byte(`{"_v":1,"project_id":"`+pid.String()+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	id, _, err := a.EnsureRepoIdentity(background())
	if err != nil || !strings.HasPrefix(id, string(ids.PrefixRepo)+"_") {
		t.Fatalf("mint the repo id: %q %v", id, err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	return id
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
