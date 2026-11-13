package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// scopeRow is a scopes row naming path.
func scopeRow(t *testing.T, path string, deleted bool) row.Row {
	t.Helper()
	p, err := value.TextOf(path)
	if err != nil {
		t.Fatal(err)
	}
	return row.Row{Table: schema.TableScopes, Deleted: deleted, Fields: map[string]value.Value{schema.FieldScopePath: p}}
}

// TestGoneScopes (B3-5): after `git mv src/booking src/bookings` and a
// deleted folder, the scopes naming them are listed (the renamed one with
// its new folder and the exact scope move command, the deleted one with a
// placeholder); a scope whose folder exists and a deleted row are not; the
// HEAD commit's removed paths name the old folder's file.
func TestGoneScopes(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@example.invalid")
	}
	dir := t.TempDir()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := &exec.Cmd{Path: gitPath, Args: append([]string{"git", "-C", dir}, args...)}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	for _, f := range []string{"src/booking/a.js", "src/keep/b.js", "old/c.js"} {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-A")
	git("commit", "-q", "-m", "one")
	git("mv", "src/booking", "src/bookings")
	git("rm", "-q", "-r", "old")
	git("commit", "-q", "-m", "move")

	ctx := context.Background()
	rows := []row.Row{scopeRow(t, "src/booking", false), scopeRow(t, "src/keep", false), scopeRow(t, "old", false), scopeRow(t, "src/gone", true)}
	got := goneScopes(ctx, dir, rows)
	if len(got) != 2 || got[0].Path != "src/booking" || got[0].RenamedTo != "src/bookings" || got[1].Path != "old" || got[1].RenamedTo != "" {
		t.Fatalf("goneScopes = %+v", got)
	}
	if l := got[0].Line(); !strings.Contains(l, "lore scope move --old src/booking --new src/bookings") {
		t.Fatalf("renamed line = %q", l)
	}
	if l := got[1].Line(); !strings.Contains(l, "lore scope move --old old --new <new-path>") {
		t.Fatalf("deleted line = %q", l)
	}
	removed := HeadRemovedPaths(ctx, dir)
	if !slices.Contains(removed, "src/booking/a.js") || !slices.Contains(removed, "old/c.js") {
		t.Fatalf("HeadRemovedPaths = %v", removed)
	}
}
