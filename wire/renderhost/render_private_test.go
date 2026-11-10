package renderhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// Store id of the private render test (arbitrary sto_ id).
const privTestInnerID = "sto_01a1a5aca7f57659be7320c5f4b700c1"

// privApp is an App over a fresh git repo with one internal store
// "inner" holding a project and one memory; it returns the App and the
// repo folder.
func privApp(t *testing.T) (*wire.App, string) {
	t.Helper()
	ctx := context.Background()
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	yaml := "stores:\n  - name: inner\n    url: " + internalStoreAt(t, privTestInnerID, rowfs.VisibilityInternal) + "\n"
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(attach.FileName)), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := wire.New(wire.Options{RepoDir: repo, Home: t.TempDir(), FlagActor: "human:alice@example.com",
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	for _, target := range []store.Source{store.SourceRepo, "inner"} {
		p, cerr := a.Create(ctx, target, schema.TableProjects, map[string]value.Value{"name": text(t, "p-"+string(target))})
		if cerr != nil {
			t.Fatal(cerr)
		}
		if target == "inner" {
			if _, cerr = a.Create(ctx, target, schema.TableMemories, map[string]value.Value{
				"project_id": text(t, p.Row.ID.String()), "kind": text(t, schema.MemoryKindCore),
				"body": text(t, "internal note"), "source_kind": text(t, "manual")}); cerr != nil {
				t.Fatal(cerr)
			}
		}
	}
	return a, repo
}

// TestRenderPrivateFiles (later:111, later:46 private-summary line):
// --dry-run prints the private file and writes nothing (no render dir, the
// user's CLAUDE.local.md untouched); a real render writes the store file
// and the index, prunes a stale sto_ file and the v0.1.17 single file,
// keeps a file lore does not own, and when no target tool has a private
// file it keeps a non-target CLAUDE.local.md's user lines as they are and
// does not name that file on the summary line.
func TestRenderPrivateFiles(t *testing.T) {
	ctx := context.Background()
	a, repo := privApp(t)
	home := t.TempDir()
	h := New(Options{App: a, Home: home})
	const localName = "CLAUDE.local.md"
	local := filepath.Join(repo, localName)
	const userLines = "# my notes\nuse the staging db\n"
	if err := os.WriteFile(local, []byte(userLines), 0o600); err != nil {
		t.Fatal(err)
	}

	var w strings.Builder
	if err := h.renderPrivate(ctx, &w, repo, true, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "(private, not written: --dry-run)") || !strings.Contains(w.String(), "internal note") {
		t.Fatalf("dry run printed %q", w.String())
	}
	if _, err := os.Stat(filepath.Join(home, renderDirName)); !os.IsNotExist(err) {
		t.Fatalf("dry run created the render dir: %v", err)
	}
	if got, _ := readUnder(t, repo, localName); string(got) != userLines {
		t.Fatalf("dry run changed CLAUDE.local.md: %q", got)
	}

	p, err := h.planPrivate(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(p.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(p.dir, "sto_01a1a5aca7f57659be7320c5f4b700ff"+privateFileExt)
	mine := filepath.Join(p.dir, "notes.md")
	old := filepath.Join(filepath.Dir(p.dir), p.projectID+privateFileExt)
	for _, f := range []string{stale, mine, old} {
		if err = os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w.Reset()
	if err = h.renderPrivate(ctx, &w, repo, false, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(p.dir, privTestInnerID+privateFileExt))
	if err != nil || !strings.Contains(string(body), "internal note") {
		t.Fatalf("store file %q %v", body, err)
	}
	if _, err = os.Stat(filepath.Join(p.dir, privateIndexFile)); err != nil {
		t.Fatalf("index: %v", err)
	}
	for _, f := range []string{stale, old} {
		if _, err = os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("%s not pruned: %v", f, err)
		}
	}
	if _, err = os.Stat(mine); err != nil {
		t.Fatalf("a file lore does not own was removed: %v", err)
	}
	if got, _ := readUnder(t, repo, localName); string(got) != userLines {
		t.Fatalf("non-target CLAUDE.local.md changed: %q", got)
	}
	line := w.String()
	if !strings.Contains(line, "private context") || strings.Contains(line, "CLAUDE.local.md") || !strings.Contains(line, "no target tool has a private file") {
		t.Fatalf("summary line %q", line)
	}
}

// readUnder reads rel through a root at dir, so a test read stays inside
// the test's own temp folder.
func readUnder(t *testing.T, dir, rel string) ([]byte, error) {
	t.Helper()
	rt, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rt.Close() }() // a read-only root; the read reports its own error
	return rt.ReadFile(rel)
}
