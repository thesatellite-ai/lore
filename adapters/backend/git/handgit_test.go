package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// testHandGit is a hand-git setup with a driver and a hook block.
var testHandGit = HandGit{Driver: "lore merge-driver %O %A %B %P", Hook: []string{"echo checked"}}

// cloneSetUp is clone opened by an explicit setup or sync run
// (WithGitSetUp), which sets the clone up for plain git on open.
func cloneSetUp(t *testing.T, remote string, opts ...Option) *Backend {
	t.Helper()
	b, err := OpenClone(WithGitSetUp(context.Background()), remote, filepath.Join(t.TempDir(), "clones", "host", "store"), ".", store.OpenOptions{}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// TestHandGitSetsUpTheClone: merge driver in .git/config, `*.json
// merge=lore` instead of `-merge`, lore's block in .git/hooks/pre-commit
// (executable), no core.hooksPath; every step said once, nothing on the
// next open.
func TestHandGitSetsUpTheClone(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	b := cloneSetUp(t, remote, WithHandGit(testHandGit))
	if got := gitT(t, b.Dir(), "config", "--local", "--get", "merge.lore.driver"); got != testHandGit.Driver {
		t.Errorf("merge.lore.driver = %q", got)
	}
	attrs, err := os.ReadFile(filepath.Join(b.Dir(), ".git", "info", "attributes"))
	if err != nil || !strings.Contains(string(attrs), rowAttributesDriver) || strings.Contains(string(attrs), rowAttributesNoDriver) {
		t.Errorf("attributes = %q %v", attrs, err)
	}
	hook := filepath.Join(b.Dir(), ".git", "hooks", "pre-commit")
	body, err := os.ReadFile(hook)
	if err != nil || !strings.HasPrefix(string(body), hookShebang+"\n"+hookBlockBegin) || !strings.Contains(string(body), "echo checked") {
		t.Fatalf("pre-commit = %q %v", body, err)
	}
	if st, _ := os.Stat(hook); st.Mode().Perm()&0o100 == 0 {
		t.Errorf("the hook is not executable: %v", st.Mode())
	}
	if _, err := tryGit(t, b.Dir(), "config", "--local", "--get", "core.hooksPath"); err == nil {
		t.Error("core.hooksPath was set although nothing global bypasses .git/hooks")
	}
	if len(b.HandGitSetUp()) != 4 {
		t.Errorf("set up = %v, want driver name, driver, attributes, hook", b.HandGitSetUp())
	}
	again, err := OpenClone(WithGitSetUp(t.Context()), remote, b.Dir(), ".", store.OpenOptions{}, WithHandGit(testHandGit))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Close() }()
	if s := again.HandGitSetUp(); len(s) != 0 {
		t.Errorf("a second open set up %v again", s)
	}
}

// TestHandGitKeepsAPersonsHook: a person's own pre-commit lines stay
// around lore's block, and a changed block is replaced in place.
func TestHandGitKeepsAPersonsHook(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	b := clone(t, remote)
	hook := filepath.Join(b.Dir(), ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "#!/bin/sh\necho mine\n"
	if err := os.WriteFile(hook, []byte(mine), 0o755); err != nil {
		t.Fatal(err)
	}
	b.cfg.handGit = testHandGit
	if err := b.ensurePreCommit(testHandGit.Hook); err != nil {
		t.Fatal(err)
	}
	if err := b.ensurePreCommit([]string{"echo checked v2"}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(hook)
	if !strings.HasPrefix(string(body), mine) || strings.Count(string(body), hookBlockBegin) != 1 || !strings.Contains(string(body), "echo checked v2") || strings.Contains(string(body), "echo checked\n") {
		t.Fatalf("pre-commit = %q", body)
	}
}

// TestHandGitGlobalHooksPath: a global core.hooksPath would make git skip
// .git/hooks, so the clone's own config points back at it (D-g).
func TestHandGitGlobalHooksPath(t *testing.T) {
	remote := newRemote(t, true)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[core]\n\thooksPath = "+filepath.Join(home, "hooks")+"\n[user]\n\tname = T\n\temail = t@example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := cloneSetUp(t, remote, WithHandGit(testHandGit))
	got := gitT(t, b.Dir(), "config", "--local", "--get", "core.hooksPath")
	if got != filepath.ToSlash(filepath.Join(b.Dir(), ".git", "hooks")) {
		t.Fatalf("core.hooksPath = %q", got)
	}
}

// TestNoDriverKeepsMinusMerge: without WithHandGit a clone keeps `*.json
// -merge` (git never text-merges a row) once a write set it up.
func TestNoDriverKeepsMinusMerge(t *testing.T) {
	t.Parallel()
	b, g := clone(t, newRemote(t, true)), newGen(t)
	write(t, b, store.Change{Row: g.row(newID(t), "x", hlc.HLC{})})
	attrs, err := os.ReadFile(filepath.Join(b.Dir(), ".git", "info", "attributes"))
	if err != nil || !strings.Contains(string(attrs), rowAttributesNoDriver) {
		t.Fatalf("attributes = %q %v", attrs, err)
	}
}

// TestReadOpenSetsNothingUp (owner bug 6): an open that is not an explicit
// setup or sync (a read) writes no merge driver, no attributes, no hook
// into the clone; the first write sets it up and says so (HandGit.Say)
// before its commit, once.
func TestReadOpenSetsNothingUp(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	var said [][]string
	var tipWhenSaid string
	hg := testHandGit
	hg.Say = func(_ context.Context, dir string, set []string) {
		said = append(said, set)
		tipWhenSaid, _ = tryGit(t, dir, "rev-parse", "HEAD")
	}
	b, g := clone(t, remote, WithHandGit(hg)), newGen(t)
	if _, err := tryGit(t, b.Dir(), "config", "--local", "--get", "merge.lore.driver"); err == nil {
		t.Fatal("a read's open configured the merge driver")
	}
	if _, err := os.Stat(filepath.Join(b.Dir(), ".git", "info", "attributes")); err == nil {
		t.Fatal("a read's open wrote .git/info/attributes")
	}
	if _, err := os.Stat(filepath.Join(b.Dir(), ".git", "hooks", "pre-commit")); err == nil {
		t.Fatal("a read's open wrote .git/hooks/pre-commit")
	}
	if s := b.HandGitSetUp(); len(s) != 0 {
		t.Fatalf("a read's open reports a set-up: %v", s)
	}
	before, _ := tryGit(t, b.Dir(), "rev-parse", "HEAD")
	write(t, b, store.Change{Row: g.row(newID(t), "x", hlc.HLC{})})
	write(t, b, store.Change{Row: g.row(newID(t), "y", hlc.HLC{})})
	if len(said) != 1 || len(said[0]) != 4 {
		t.Fatalf("the writes said %v, want one line of 4 set-up steps", said)
	}
	if tipWhenSaid != before {
		t.Fatalf("the set-up was said after the write committed (tip %q, before %q)", tipWhenSaid, before)
	}
	if got := gitT(t, b.Dir(), "config", "--local", "--get", "merge.lore.driver"); got != hg.Driver {
		t.Errorf("after a write merge.lore.driver = %q", got)
	}
}

// TestOwnMergeAttributes: lore's own three-way merge passes an attributes
// file that unsets merge for row files, so a clone never set up for plain
// git (a read cloned it, owner bug 6) still leaves every row both sides
// changed to the Merger instead of text-merging it.
func TestOwnMergeAttributes(t *testing.T) {
	t.Parallel()
	b := clone(t, newRemote(t, true))
	p, err := b.mergeAttributes()
	if err != nil {
		t.Fatal(err)
	}
	if rel, rerr := filepath.Rel(filepath.Join(b.Dir(), ".git"), filepath.FromSlash(p)); rerr != nil || strings.HasPrefix(rel, "..") || strings.HasPrefix(rel, "info") {
		t.Fatalf("the merge attributes file %s is not in the clone's lore state folder", p)
	}
	got := gitT(t, b.Dir(), "-c", "core.attributesFile="+p, "check-attr", "merge", "--", "x/row.json")
	if !strings.Contains(got, "merge: unset") {
		t.Fatalf("check-attr = %q, want merge unset", got)
	}
}

// TestReadOpenWritesNoIdentity (owner bug 6): an open of an existing clone
// that is not an explicit setup or sync writes no user.name/user.email;
// the first write does, and says so through WithCloneIdentitySay.
func TestReadOpenWritesNoIdentity(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	first := clone(t, remote) // lore makes the clone (no identity option here)
	dir := first.Dir()
	want := CloneIdentity{Signature: Signature{Name: "Pat Person", Email: "pat@work.example"}, Why: "test"}
	var said []CloneIdentity
	b, err := OpenClone(context.Background(), remote, dir, ".", store.OpenOptions{},
		WithCloneIdentity(func(context.Context, string) CloneIdentity { return want }),
		WithCloneIdentitySay(func(_ context.Context, _ string, id CloneIdentity) { said = append(said, id) }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	if _, ok := b.IdentitySetUp(); ok {
		t.Fatal("a read's open of an existing clone wrote its identity")
	}
	if v, _ := LocalConfig(t.Context(), dir, ConfigUserEmail); v != "" {
		t.Fatalf("a read's open set user.email = %q", v)
	}
	g := newGen(t)
	write(t, b, store.Change{Row: g.row(newID(t), "x", hlc.HLC{})})
	if v, _ := LocalConfig(t.Context(), dir, ConfigUserEmail); v != want.Email || len(said) != 1 || said[0].Email != want.Email {
		t.Fatalf("after a write user.email = %q, said %+v", v, said)
	}
}
