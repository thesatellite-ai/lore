// Package partialclone makes a blobless (--filter=blob:none) clone for
// tests: the clone holds every commit and tree but only the blobs of its
// tip, as a partial clone of a store does once lore makes them (store
// growth SG-3). Tests use it to prove a blob read that the clone cannot
// satisfy is an error, never "no such row" (fail closed, devco:D-997).
//
// The origin is reached through a file:// URL with uploadpack.allowFilter
// on: a plain path takes git's local-clone shortcut, which ignores the
// filter and copies every object.
package partialclone

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Repo is a blobless clone and the origin it was made from.
type Repo struct {
	// Origin is the work tree the clone was made from.
	Origin string
	// Clone is the blobless clone's work tree.
	Clone string
	// Blobs are the blob ids of each version written, in order; every one
	// but the last is missing from Clone.
	Blobs []string
}

// gitEnv is a git environment with no system or global config and a
// fixed identity.
func gitEnv(home string) []string {
	return append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "HOME="+home, "LC_ALL=C",
		"GIT_AUTHOR_NAME=Ann Example", "GIT_AUTHOR_EMAIL=ann@example.test", "GIT_COMMITTER_NAME=Ann Example", "GIT_COMMITTER_EMAIL=ann@example.test")
}

// Git runs git in dir with the package's environment and fails the test
// on error; it returns the trimmed output.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Make commits each body in bodies to path (slash-separated, relative to
// the origin's top) as one commit per version, then clones the origin
// with --filter=blob:none. It skips the test when git is not installed.
func Make(t testing.TB, path string, bodies ...string) Repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	r := Repo{Origin: filepath.Join(root, "origin"), Clone: filepath.Join(root, "clone")}
	Git(t, root, "init", "-q", "-b", "main", r.Origin)
	Git(t, r.Origin, "config", "uploadpack.allowFilter", "true")
	Git(t, r.Origin, "config", "uploadpack.allowAnySHA1InWant", "true")
	full := filepath.Join(r.Origin, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	for i, body := range bodies {
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		Git(t, r.Origin, "add", "--", path)
		Git(t, r.Origin, "commit", "-q", "-m", "version "+string(rune('1'+i)))
		r.Blobs = append(r.Blobs, Git(t, r.Origin, "rev-parse", "HEAD:"+path))
	}
	Git(t, root, "clone", "-q", "--filter=blob:none", "file://"+filepath.ToSlash(r.Origin), r.Clone)
	have := Git(t, r.Clone, "cat-file", "--batch-check", "--batch-all-objects")
	for _, b := range r.Blobs[:len(r.Blobs)-1] {
		if strings.Contains(have, b) {
			t.Fatalf("the blobless clone holds blob %s already; the test needs it missing", b)
		}
	}
	return r
}

// CutOff points the clone's origin at a folder that does not exist, so a
// lazy fetch of a missing blob fails the way it does with no network.
func (r Repo) CutOff(t testing.TB) {
	t.Helper()
	Git(t, r.Clone, "remote", "set-url", "origin", "file://"+filepath.ToSlash(filepath.Join(filepath.Dir(r.Origin), "gone")))
}
