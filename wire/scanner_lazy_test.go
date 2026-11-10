package wire

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// countingSink is a sessionSink that counts the session id reads, the
// first step of every detector build (refresh).
type countingSink struct {
	sessionSink
	n  int
	mu sync.Mutex
}

func (c *countingSink) ToolSessionIDs(ctx context.Context) ([]string, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.sessionSink.ToolSessionIDs(ctx)
}

func (c *countingSink) builds() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// TestWriteScannerLazy (SECRETS 3.8, P3-SEC-5): opening the App and
// running a read builds no detector; the first write builds it and still
// scans (a token is caught on that very write).
func TestWriteScannerLazy(t *testing.T) {
	t.Parallel()
	sink := &countingSink{}
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{RepoDir: gitRepo(t), Home: t.TempDir(), Actor: actor, Audit: sink,
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cerr := a.Close(); cerr != nil {
			t.Error(cerr)
		}
	})
	if _, err = a.Engine(background()); err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, a, opsmemory.OpList, `{}`, false); err != nil {
		t.Fatal(err)
	}
	if n := sink.builds(); n != 0 {
		t.Fatalf("open + list built the detector %d times, want 0", n)
	}
	withProject(t, a)
	if sink.builds() == 0 {
		t.Fatal("a write must build the detector")
	}
	if _, err = run(t, a, opsmemory.OpAdd, memBody(t, "token "+ghToken), false); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("a token in the first memory write: err = %v, want %s", err, errs.NeedsConfirm)
	}
}

// TestLazyScanFailsClosed (P3-SEC-5): a scan outside the write path's
// Refresh builds the detector itself; when that build fails (here a bad
// repo .gitleaks.toml) the whole non-empty text is a finding, never an
// unscanned pass.
func TestLazyScanFailsClosed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, repoGitleaksFile), []byte("[allowlist\nbroken"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := newScanner(background(), map[store.Source]store.Backend{}, withRepoGitleaks(dir), withLazyBuild())
	if err != nil {
		t.Fatalf("a lazy scanner reads nothing at construction: %v", err)
	}
	rep := s.ScanReport("deleted_reason", "plain words")
	if !rep.Degraded || len(rep.Findings) != 1 || rep.Findings[0].RuleID != scannerUnavailable {
		t.Fatalf("report = %+v, want one %s finding, degraded", rep, scannerUnavailable)
	}
	if got := s.Scan("deleted_reason", ""); len(got) != 0 {
		t.Fatalf("empty text: %+v, want no finding", got)
	}
	if err := s.Refresh(background()); !errs.Has(err, errs.ConfigInvalid) {
		t.Fatalf("Refresh (the write path) = %v, want %s", err, errs.ConfigInvalid)
	}
}
