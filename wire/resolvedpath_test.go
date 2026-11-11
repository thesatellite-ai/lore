package wire

import (
	"os"
	"path/filepath"
	"testing"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// linkedTemp is a temp folder reached through a symbolic link, the shape of
// macOS's /var -> /private/var: real is the folder, link names it. It skips
// where the test process may not create links (Windows without developer
// mode).
func linkedTemp(t *testing.T) (real, link string) {
	t.Helper()
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	return real, link
}

// TestResolvedPathMissingTail: a path that does not exist yet, under a
// folder reached through a link, resolves through its deepest existing
// folder and keeps the missing names. Before, it came back unresolved and
// so compared as outside a resolved repo root (macOS: dangling tool-file
// links refused as "outside the repository").
func TestResolvedPathMissingTail(t *testing.T) {
	t.Parallel()
	real, link := linkedTemp(t)
	for in, want := range map[string]string{
		link:                           real,
		filepath.Join(link, "missing"): filepath.Join(real, "missing"),
		filepath.Join(link, "missing", "a", "b.md"): filepath.Join(real, "missing", "a", "b.md"),
	} {
		if got := resolvedPath(in); got != want {
			t.Errorf("resolvedPath(%s) = %s, want %s", in, got, want)
		}
	}
}

// TestAsClientDataPrefixThroughLink: git names the top resolved; a project
// root spelled through a link (and not created yet) still gets its own
// data prefix, not the top's.
func TestAsClientDataPrefixThroughLink(t *testing.T) {
	t.Parallel()
	real, link := linkedTemp(t)
	if got, want := asClientDataPrefix(real, filepath.Join(link, "svc", "api")), "svc/api/"+checkDataPrefix; got != want {
		t.Fatalf("asClientDataPrefix = %q, want %q", got, want)
	}
}

// TestOriginDirThroughLink: a row made in a monorepo subfolder whose path
// goes through a link (the repo opened as /var/..., git's top
// /private/var/...) still records _o.dir; before, the two spellings made
// the subfolder look outside the repo and dir was dropped.
func TestOriginDirThroughLink(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	gitCommit(t, repo)
	if err := os.MkdirAll(filepath.Join(repo, "services", "billing"), 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(repo, link); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	a := newOriginApp(t, repo, filepath.Join(link, "services", "billing"), false)
	withProject(t, a)
	out, err := run(t, a, opsmemory.OpAdd, `{"text":"made through a link"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if o, _ := originOfFile(t, memPath(repo, idOf(t, out))); string(o["dir"]) != `"services/billing"` {
		t.Fatalf("_o.dir = %s, want \"services/billing\" (%v)", o["dir"], o)
	}
}
