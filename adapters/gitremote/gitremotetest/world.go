// Package gitremotetest builds throwaway git worlds for tests: one bare
// "origin" repository in a temp folder and any number of clones of it, each
// with a gitref.Repo and a real gitremote.Git remote ready to wrap in a
// FaultRemote. Everything lives under t.TempDir(); nothing touches $HOME or
// the network.
//
//	w := gitremotetest.NewWorld(t)
//	a, b := w.Clone("a"), w.Clone("b")
//	a.Write("refs/lore/live", "_root/tasks/t1.json", "{}")
//	a.MustPush("refs/lore/live")
//	b.Fetch()
package gitremotetest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
)

// RemoteName is the remote name every clone uses for the origin.
const RemoteName = "origin"

// DefaultBranch is the origin's default branch.
const DefaultBranch = "main"

// Sig is the signature helper commits use.
var Sig = gitref.Signature{Name: "Lore Test", Email: "lore@example.test", When: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)}

// World is a bare origin plus its clones.
type World struct {
	t testing.TB
	// Root is the temp folder holding everything.
	Root string
	// OriginDir is the bare origin repository.
	OriginDir string
	// Origin is the origin opened as a gitref.Repo (for direct inspection
	// or for simulating a writer on the server).
	Origin *gitref.Repo
}

// Clone is one clone of the origin.
type Clone struct {
	w *World
	// Name is the clone's label.
	Name string
	// Dir is its work tree.
	Dir string
	// Repo is the clone opened as a gitref.Repo.
	Repo *gitref.Repo
	// Remote is the real remote "origin".
	Remote *gitremote.Git
}

// Git runs a git command in dir for test setup, failing the test on error,
// with a fixed identity and locale and none of the caller's repo env.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	// The Cmd is built from the LookPath result rather than exec.Command
	// with a variable argument list: the program is always git, and every
	// argument goes to it as argv (no shell), as in gitref.Git.
	bin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git not found: %v", err)
	}
	cmd := &exec.Cmd{Path: bin, Args: append([]string{bin}, args...), Dir: dir}
	env := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// NewWorld creates a bare origin whose main branch holds one commit
// (README.md) and whose HEAD names main.
func NewWorld(t testing.TB) *World {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	Git(t, root, "init", "-q", "--bare", "-b", DefaultBranch, origin)
	Git(t, root, "init", "-q", "-b", DefaultBranch, seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	Git(t, seed, "add", ".")
	Git(t, seed, "commit", "-q", "-m", "seed")
	Git(t, seed, "push", "-q", origin, DefaultBranch)
	if err := os.RemoveAll(seed); err != nil { // free the name for a clone
		t.Fatal(err)
	}
	r, err := gitref.Open(context.Background(), origin, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &World{t: t, Root: root, OriginDir: origin, Origin: r}
}

// Clone makes a new clone named name.
func (w *World) Clone(name string) *Clone {
	w.t.Helper()
	dir := filepath.Join(w.Root, name)
	Git(w.t, w.Root, "clone", "-q", w.OriginDir, dir)
	r, err := gitref.Open(context.Background(), dir, nil)
	if err != nil {
		w.t.Fatal(err)
	}
	return &Clone{w: w, Name: name, Dir: dir, Repo: r, Remote: &gitremote.Git{Repo: r, Name: RemoteName}}
}

// BareClone makes a bare clone named name (the shape of a store clone).
func (w *World) BareClone(name string) *Clone {
	w.t.Helper()
	dir := filepath.Join(w.Root, name+".git")
	Git(w.t, w.Root, "clone", "-q", "--bare", w.OriginDir, dir)
	r, err := gitref.Open(context.Background(), dir, nil)
	if err != nil {
		w.t.Fatal(err)
	}
	return &Clone{w: w, Name: name, Dir: dir, Repo: r, Remote: &gitremote.Git{Repo: r, Name: RemoteName}}
}

// OriginRef returns where ref is on the origin ("" when missing).
func (w *World) OriginRef(ref string) string {
	w.t.Helper()
	id, _, err := w.Origin.ResolveRef(context.Background(), ref)
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

// Write puts one file on ref in this clone (appending history) and returns
// the new commit.
func (c *Clone) Write(ref, path, content string) string {
	c.w.t.Helper()
	res, err := c.Repo.WriteRef(context.Background(), gitref.WriteRequest{Ref: ref, Message: "test: " + path, Signature: Sig,
		Changes: gitref.Changes{Put: map[string][]byte{path: []byte(content)}}})
	if err != nil {
		c.w.t.Fatal(err)
	}
	return res.New
}

// Ref returns where ref is in this clone ("" when missing).
func (c *Clone) Ref(ref string) string {
	c.w.t.Helper()
	id, _, err := c.Repo.ResolveRef(context.Background(), ref)
	if err != nil {
		c.w.t.Fatal(err)
	}
	return id
}

// MustPush pushes the local ref to the same name on the origin as a plain
// (fast-forward) push and fails the test unless it is applied.
func (c *Clone) MustPush(ref string) {
	c.w.t.Helper()
	res, err := c.Remote.Push(context.Background(), gitremote.PushRequest{Updates: []gitremote.RefUpdate{{Src: c.Ref(ref), Dst: ref}}})
	if err != nil || !res.OK() {
		c.w.t.Fatalf("push %s from %s: %+v %v", ref, c.Name, res, err)
	}
}

// Fetch runs lore's tracking fetch (refs/lore/* and main) with prune.
func (c *Clone) Fetch() {
	c.w.t.Helper()
	if err := c.Remote.Fetch(context.Background(), gitremote.FetchRequest{Refspecs: gitremote.TrackingRefspecs(RemoteName, DefaultBranch), Prune: true}); err != nil {
		c.w.t.Fatal(err)
	}
}

// SyncRef fetches and points the local ref at the origin's copy (from its
// tracking ref), as a clone that has caught up would hold it. Use it to give
// fresh clones a shared starting point before a race.
func (c *Clone) SyncRef(ref string) {
	c.w.t.Helper()
	c.Fetch()
	track, ok := gitremote.TrackingRef(RemoteName, ref)
	if !ok {
		c.w.t.Fatalf("no tracking ref for %s", ref)
	}
	if err := c.Repo.UpdateRef(context.Background(), ref, c.Ref(track), c.Ref(ref)); err != nil {
		c.w.t.Fatal(err)
	}
}
