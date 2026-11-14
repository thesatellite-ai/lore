package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// dirStore is a repo store backend at dir (the dirBackend wrap needs).
type dirStore struct {
	store.Backend
	dir string
}

func (d dirStore) Dir() string { return d.dir }

// TestLiveSessionNeedsProject (FP-7): the repo store of a git repo with no
// .lore/ (one a store links by its remote, opened read-only) gets no live
// session: a read there takes no live lock, adds no refspec to .git/config
// and fetches nothing, so .git/lore is never created. Once .lore/ exists
// (a write's implicit setup makes it during the open) the session opens.
func TestLiveSessionNeedsProject(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	t.Setenv(EnvLive, "")
	t.Setenv(EnvSync, "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	cfgBefore, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	h := &liveHost{cfg: LiveConfig{Home: filepath.Join(home, ".lore"), WorkDir: dir, Version: "0.1.18", Stderr: &strings.Builder{}}}
	run := &liveRun{h: h, sessions: map[string]*liveSession{}}
	ctx := withRun(context.Background(), run)
	b := h.wrap(store.SourceRepo, dirStore{Backend: memstore.NewData().Open(store.OpenOptions{}), dir: dir}).(*liveBackend)

	if s := b.session(ctx); s != nil {
		s.release()
		t.Fatal("a repo with no .lore got a live session")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", liveStateDirName)); !os.IsNotExist(err) {
		t.Fatalf("a repo with no .lore got .git/%s: %v", liveStateDirName, err)
	}
	if cfg, _ := os.ReadFile(filepath.Join(dir, ".git", "config")); string(cfg) != string(cfgBefore) {
		t.Fatalf("a repo with no .lore had .git/config changed:\n%s", cfg)
	}

	// an empty .lore/ (an earlier lore run left it, D-20) is no project
	if err := os.MkdirAll(filepath.Join(dir, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	if s := b.session(ctx); s != nil {
		s.release()
		t.Fatal("a repo with an empty .lore got a live session")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".lore", "data"), 0o750); err != nil {
		t.Fatal(err)
	}
	s := b.session(ctx)
	if s == nil {
		t.Fatal("a lore project got no live session")
	}
	s.release()
}
