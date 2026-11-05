package gitref

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// testSig is the fixed identity every test commit uses.
var testSig = Signature{Name: "Lore Test", Email: "lore@example.test", When: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)}

// gitCmd builds a git command for test setup from the resolved git binary;
// each argument is its own argv entry, no shell parses them.
func gitCmd(t *testing.T, dir string, args ...string) *exec.Cmd {
	t.Helper()
	bin, err := exec.LookPath(gitBinary)
	if err != nil {
		t.Fatal(err)
	}
	return &exec.Cmd{Path: bin, Args: append([]string{bin}, args...), Dir: dir}
}

// git runs a git command for test setup and fails the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := gitCmd(t, dir, args...)
	cmd.Env = append(cleanEnv(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoKind is one of the repository shapes lore writes to.
type repoKind string

const (
	kindNormal   repoKind = "normal"   // a clone with a work tree
	kindBare     repoKind = "bare"     // a bare store clone
	kindWorktree repoKind = "worktree" // a linked worktree of a normal repo
	kindSubdir   repoKind = "subdir"   // opened from a subfolder of a work tree
)

var allKinds = []repoKind{kindNormal, kindBare, kindWorktree, kindSubdir}

// newRepo makes a repository of the given kind whose main branch has one
// commit holding seed.txt, and opens it.
func newRepo(t *testing.T, kind repoKind) *Repo {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	git(t, src, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "seed.txt"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "keep.txt"), []byte("k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", ".")
	git(t, src, "commit", "-q", "-m", "seed")
	dir := src
	switch kind {
	case kindBare:
		dir = filepath.Join(root, "bare.git")
		git(t, root, "clone", "-q", "--bare", src, dir)
	case kindWorktree:
		dir = filepath.Join(root, "wt")
		git(t, src, "worktree", "add", "-q", "-b", "wtbranch", dir)
	case kindSubdir:
		dir = filepath.Join(src, "sub")
	case kindNormal:
		// the clone itself
	}
	r, err := Open(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Bare() != (kind == kindBare) {
		t.Fatalf("%s: Bare() = %v", kind, r.Bare())
	}
	return r
}

func TestOpenRejectsNonRepo(t *testing.T) {
	if _, err := Open(context.Background(), t.TempDir(), nil); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("err = %v, want ErrNotRepo", err)
	}
}

func TestReadTreeEveryRepoKind(t *testing.T) {
	ctx := context.Background()
	for _, kind := range allKinds {
		t.Run(string(kind), func(t *testing.T) {
			r := newRepo(t, kind)
			all, err := r.ReadTree(ctx, "refs/heads/main", "")
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"seed.txt": "seed\n", "sub/keep.txt": "k\n"}
			if len(all) != len(want) {
				t.Fatalf("got %d files: %v", len(all), keys(all))
			}
			for p, c := range want {
				if string(all[p]) != c {
					t.Fatalf("%s = %q want %q", p, all[p], c)
				}
			}
			// Paths are root-relative even when opened from a subfolder.
			sub, err := r.ReadTree(ctx, "main", "sub")
			if err != nil || len(sub) != 1 || string(sub["sub/keep.txt"]) != "k\n" {
				t.Fatalf("prefix read = %v, %v", keys(sub), err)
			}
			none, err := r.ReadTree(ctx, "main", "missing/dir")
			if err != nil || len(none) != 0 {
				t.Fatalf("missing prefix = %v, %v", keys(none), err)
			}
			if _, nerr := r.ReadTree(ctx, "refs/lore/nothing", ""); !errors.Is(nerr, ErrNotFound) {
				t.Fatalf("missing rev err = %v", nerr)
			}
			b, ok, err := r.ReadFile(ctx, "main", "seed.txt")
			if err != nil || !ok || string(b) != "seed\n" {
				t.Fatalf("ReadFile = %q %v %v", b, ok, err)
			}
			if _, ok, err := r.ReadFile(ctx, "main", "nope.txt"); err != nil || ok {
				t.Fatalf("ReadFile missing = %v %v", ok, err)
			}
		})
	}
}

func TestReadTreeRefusesSymlink(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t, kindNormal)
	git(t, r.Dir(), "checkout", "-q", "-b", "withlink")
	if err := os.Symlink("seed.txt", filepath.Join(r.Dir(), "link")); err != nil {
		t.Skip("no symlinks on this file system")
	}
	git(t, r.Dir(), "add", "link")
	git(t, r.Dir(), "commit", "-q", "-m", "link")
	if _, err := r.ReadTree(ctx, "withlink", ""); !errors.Is(err, ErrUnsupportedEntry) {
		t.Fatalf("err = %v, want ErrUnsupportedEntry", err)
	}
	// A prefix that avoids the symlink still reads.
	if files, err := r.ReadTree(ctx, "withlink", "sub"); err != nil || len(files) != 1 {
		t.Fatalf("prefix read = %v %v", keys(files), err)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// parents returns the parent ids of commit oid.
func parents(t *testing.T, r *Repo, oid string) []string {
	t.Helper()
	out := git(t, r.Dir(), "rev-list", "--parents", "-n", "1", oid)
	return strings.Fields(out)[1:]
}

// TestWriteRefMatrix covers repo kind × target ref × first write or not ×
// history mode × kind of change, checking tree content, parents and no-op.
func TestWriteRefMatrix(t *testing.T) {
	ctx := context.Background()
	refs := []string{"refs/lore/live", "refs/lore/signals/main", "refs/heads/store-branch"}
	histories := []History{HistoryAppend, HistoryReplace}
	for _, kind := range allKinds {
		for _, ref := range refs {
			for _, hist := range histories {
				name := fmt.Sprintf("%s/%s/hist%d", kind, ref, hist)
				t.Run(name, func(t *testing.T) {
					r := newRepo(t, kind)
					// First write: the ref does not exist → parentless commit.
					w1, err := r.WriteRef(ctx, WriteRequest{Ref: ref, History: hist, Message: "first", Signature: testSig,
						Changes: Changes{Put: map[string][]byte{"_root/tasks/t1.json": []byte("{\"a\":1}\n"), "_root/tasks/t2.json": []byte("{}\n")}}})
					if err != nil {
						t.Fatal(err)
					}
					if !w1.Changed || w1.Old != "" || len(parents(t, r, w1.New)) != 0 {
						t.Fatalf("first write = %+v parents %v", w1, parents(t, r, w1.New))
					}
					// Same content again: no commit, ref unchanged (C9).
					again, err := r.WriteRef(ctx, WriteRequest{Ref: ref, History: hist, Message: "again", Signature: testSig,
						Changes: Changes{Put: map[string][]byte{"_root/tasks/t1.json": []byte("{\"a\":1}\n")}}})
					if err != nil || again.Changed || again.New != w1.New {
						t.Fatalf("no-op write = %+v %v", again, err)
					}
					// Edit one, delete another, delete a path that was never there.
					w2, err := r.WriteRef(ctx, WriteRequest{Ref: ref, History: hist, Message: "second", Signature: testSig,
						Changes: Changes{Put: map[string][]byte{"_root/tasks/t1.json": []byte("{\"a\":2}\n"), "web/rules/r1.json": []byte("r\n")},
							Delete: []string{"_root/tasks/t2.json", "_root/tasks/never.json"}}})
					if err != nil || !w2.Changed || w2.Old != w1.New {
						t.Fatalf("second write = %+v %v", w2, err)
					}
					wantParents := []string{w1.New}
					if hist == HistoryReplace {
						wantParents = nil
					}
					if got := parents(t, r, w2.New); !slices.Equal(got, wantParents) {
						t.Fatalf("parents = %v want %v", got, wantParents)
					}
					files, err := r.ReadTree(ctx, ref, "")
					if err != nil {
						t.Fatal(err)
					}
					if got := keys(files); !slices.Equal(got, []string{"_root/tasks/t1.json", "web/rules/r1.json"}) {
						t.Fatalf("tree = %v", got)
					}
					if string(files["_root/tasks/t1.json"]) != "{\"a\":2}\n" {
						t.Fatalf("t1 = %q", files["_root/tasks/t1.json"])
					}
					// Deleting everything leaves an empty tree, not an error.
					w3, err := r.WriteRef(ctx, WriteRequest{Ref: ref, History: hist, Message: "clear", Signature: testSig,
						Changes: Changes{Delete: []string{"_root/tasks/t1.json", "web/rules/r1.json"}}})
					if err != nil || !w3.Changed {
						t.Fatalf("clear = %+v %v", w3, err)
					}
					if files, err := r.ReadTree(ctx, ref, ""); err != nil || len(files) != 0 {
						t.Fatalf("after clear = %v %v", keys(files), err)
					}
					// The commit carries the explicit signature, not git config.
					if who := git(t, r.Dir(), "log", "-1", "--format=%an <%ae> %at %cn", w3.New); who != "Lore Test <lore@example.test> 1793613600 Lore Test" {
						t.Fatalf("signature = %q", who)
					}
				})
			}
		}
	}
}

func TestWriteRefLeavesWorkTreeAndIndexAlone(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t, kindNormal)
	if err := os.WriteFile(filepath.Join(r.Dir(), "seed.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, r.Dir(), "add", "seed.txt")
	before := git(t, r.Dir(), "status", "--porcelain")
	head := git(t, r.Dir(), "rev-parse", "HEAD")
	if _, err := r.WriteRef(ctx, WriteRequest{Ref: "refs/lore/live", Message: "m", Signature: testSig,
		Changes: Changes{Put: map[string][]byte{"_root/x.json": []byte("{}")}}}); err != nil {
		t.Fatal(err)
	}
	if after := git(t, r.Dir(), "status", "--porcelain"); after != before {
		t.Fatalf("status changed: %q → %q", before, after)
	}
	if git(t, r.Dir(), "rev-parse", "HEAD") != head {
		t.Fatal("HEAD moved")
	}
}

func TestMakeCommitOnTrackingRefDoesNotMoveAnything(t *testing.T) {
	// C6: a landing builds on the fetched tracking ref and only returns the
	// commit; no local ref moves.
	ctx := context.Background()
	r := newRepo(t, kindNormal)
	main := git(t, r.Dir(), "rev-parse", "refs/heads/main")
	git(t, r.Dir(), "update-ref", "refs/remotes/origin/main", main)
	refsBefore := git(t, r.Dir(), "for-each-ref")
	c, err := r.MakeCommit(ctx, CommitRequest{Base: "refs/remotes/origin/main", Message: "lore: land 1 row", Signature: testSig,
		Changes: Changes{Put: map[string][]byte{".lore/data/tasks/t.json": []byte("{}\n")}, Delete: []string{"seed.txt"}}})
	if err != nil || !c.Changed || c.Base != main {
		t.Fatalf("MakeCommit = %+v %v", c, err)
	}
	if got := parents(t, r, c.OID); !slices.Equal(got, []string{main}) {
		t.Fatalf("parents = %v", got)
	}
	if git(t, r.Dir(), "for-each-ref") != refsBefore {
		t.Fatal("MakeCommit moved a ref")
	}
	files, err := r.ReadTree(ctx, c.OID, "")
	if err != nil || !slices.Equal(keys(files), []string{".lore/data/tasks/t.json", "sub/keep.txt"}) {
		t.Fatalf("tree = %v %v", keys(files), err)
	}
	// A base without changes and no base at all are no-ops.
	if c2, err := r.MakeCommit(ctx, CommitRequest{Base: "main", Message: "m", Signature: testSig}); err != nil || c2.Changed || c2.OID != main {
		t.Fatalf("empty change = %+v %v", c2, err)
	}
	if c3, err := r.MakeCommit(ctx, CommitRequest{Message: "m", Signature: testSig}); err != nil || c3.Changed || c3.OID != "" {
		t.Fatalf("nothing at all = %+v %v", c3, err)
	}
}

func TestMakeCommitRefusals(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t, kindBare)
	put := func(p string) Changes { return Changes{Put: map[string][]byte{p: []byte("x")}} }
	cases := []struct {
		name string
		req  CommitRequest
		want error
	}{
		{"no signature", CommitRequest{Changes: put("a")}, ErrNoSignature},
		{"no email", CommitRequest{Changes: put("a"), Signature: Signature{Name: "n"}}, ErrNoSignature},
		{"absolute path", CommitRequest{Changes: put("/etc/passwd"), Signature: testSig}, ErrBadPath},
		{"dot dot", CommitRequest{Changes: put("a/../../b"), Signature: testSig}, ErrBadPath},
		{"dot git", CommitRequest{Changes: put(".git/config"), Signature: testSig}, ErrBadPath},
		{"dot GIT", CommitRequest{Changes: put("x/.GIT/hooks/post-commit"), Signature: testSig}, ErrBadPath},
		{"empty component", CommitRequest{Changes: put("a//b"), Signature: testSig}, ErrBadPath},
		{"empty", CommitRequest{Changes: put(""), Signature: testSig}, ErrBadPath},
		{"put and delete", CommitRequest{Changes: Changes{Put: map[string][]byte{"a": nil}, Delete: []string{"a"}}, Signature: testSig}, ErrBadPath},
		{"file becomes folder", CommitRequest{Base: "main", Changes: put("seed.txt/x"), Signature: testSig}, ErrPathClash},
		{"folder becomes file", CommitRequest{Base: "main", Changes: put("sub"), Signature: testSig}, ErrPathClash},
		{"clash inside one change", CommitRequest{Changes: Changes{Put: map[string][]byte{"a": nil, "a/b": nil}}, Signature: testSig}, ErrPathClash},
		{"missing base", CommitRequest{Base: "refs/lore/none", Changes: put("a"), Signature: testSig}, ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.MakeCommit(ctx, tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	// Replacing the clashing file in the same change is allowed.
	if c, err := r.MakeCommit(ctx, CommitRequest{Base: "main", Signature: testSig, Message: "m",
		Changes: Changes{Put: map[string][]byte{"seed.txt/x": []byte("x")}, Delete: []string{"seed.txt"}}}); err != nil || !c.Changed {
		t.Fatalf("delete-then-nest = %+v %v", c, err)
	}
}

func TestRefValidation(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t, kindNormal)
	for _, bad := range []string{"main", "heads/main", "refs/lore/..x", "refs/lore/live.lock", "refs/lore/a b", ""} {
		if _, _, err := r.ResolveRef(ctx, bad); !errors.Is(err, ErrBadRef) {
			t.Errorf("ResolveRef(%q) err = %v", bad, err)
		}
		if err := r.UpdateRef(ctx, bad, "x", ""); !errors.Is(err, ErrBadRef) {
			t.Errorf("UpdateRef(%q) err = %v", bad, err)
		}
	}
	// ResolveRef is exact: a ref below the name is not the name.
	main := git(t, r.Dir(), "rev-parse", "main")
	git(t, r.Dir(), "update-ref", "refs/lore/live/main", main)
	if _, ok, err := r.ResolveRef(ctx, "refs/lore/live"); err != nil || ok {
		t.Fatalf("prefix matched as exact ref: %v %v", ok, err)
	}
	if id, ok, err := r.ResolveRef(ctx, "refs/lore/live/main"); err != nil || !ok || id != main {
		t.Fatalf("exact ref = %v %v %v", id, ok, err)
	}
	refs, err := r.ListRefs(ctx, "refs/lore/")
	if err != nil || len(refs) != 1 || refs["refs/lore/live/main"] != main {
		t.Fatalf("ListRefs = %v %v", refs, err)
	}
}

func TestCASLosesCleanly(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []repoKind{kindNormal, kindBare} {
		t.Run(string(kind), func(t *testing.T) {
			r := newRepo(t, kind)
			ref := "refs/lore/live"
			first, err := r.WriteRef(ctx, WriteRequest{Ref: ref, Message: "a", Signature: testSig, Changes: Changes{Put: map[string][]byte{"a": []byte("a")}}})
			if err != nil {
				t.Fatal(err)
			}
			// Writer A builds on `first`; writer B moves the ref first.
			cA, err := r.MakeCommit(ctx, CommitRequest{Base: first.New, Message: "A", Signature: testSig, Changes: Changes{Put: map[string][]byte{"b": []byte("A")}}})
			if err != nil {
				t.Fatal(err)
			}
			wB, err := r.WriteRef(ctx, WriteRequest{Ref: ref, Message: "B", Signature: testSig, Changes: Changes{Put: map[string][]byte{"b": []byte("B")}}})
			if err != nil {
				t.Fatal(err)
			}
			err = r.UpdateRef(ctx, ref, cA.OID, first.New)
			var cas *CASError
			if !errors.As(err, &cas) || !errors.Is(err, ErrRefMoved) || cas.Expected != first.New || cas.Actual != wB.New {
				t.Fatalf("lost CAS err = %v (%+v)", err, cas)
			}
			if now, _, _ := r.ResolveRef(ctx, ref); now != wB.New {
				t.Fatalf("loser moved the ref to %s", now)
			}
			// "Must not exist" loses against an existing ref.
			if err := r.UpdateRef(ctx, ref, cA.OID, ""); !errors.As(err, &cas) || cas.Actual != wB.New {
				t.Fatalf("create-only CAS err = %v", err)
			}
			// Delete needs the right old id too.
			if err := r.DeleteRef(ctx, ref, first.New); !errors.As(err, &cas) {
				t.Fatalf("stale delete err = %v", err)
			}
			if err := r.DeleteRef(ctx, ref, ""); err == nil {
				t.Fatal("blind delete allowed")
			}
			if err := r.DeleteRef(ctx, ref, wB.New); err != nil {
				t.Fatal(err)
			}
			if _, ok, _ := r.ResolveRef(ctx, ref); ok {
				t.Fatal("ref still there after delete")
			}
			// A delete of a missing ref is a CAS loss with Actual "".
			if err := r.DeleteRef(ctx, ref, wB.New); !errors.As(err, &cas) || cas.Actual != "" {
				t.Fatalf("delete missing err = %v", err)
			}
		})
	}
}

// TestConcurrentWritersNeverLoseACommit runs many writers against one ref,
// each retrying on CAS loss; every write must end up in the ref's history
// and in its tree, with exactly one commit per writer.
func TestConcurrentWritersNeverLoseACommit(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []repoKind{kindNormal, kindBare} {
		t.Run(string(kind), func(t *testing.T) {
			r := newRepo(t, kind)
			const writers = 12
			ref := "refs/lore/live"
			var wg sync.WaitGroup
			var mu sync.Mutex
			losses := 0
			errs := make(chan error, writers)
			for i := range writers {
				wg.Go(func() {
					for {
						_, err := r.WriteRef(ctx, WriteRequest{Ref: ref, Message: fmt.Sprintf("w%d", i), Signature: testSig,
							Changes: Changes{Put: map[string][]byte{fmt.Sprintf("_root/tasks/t%02d.json", i): []byte("{}")}}})
						if errors.Is(err, ErrRefMoved) {
							mu.Lock()
							losses++
							mu.Unlock()
							continue
						}
						errs <- err
						return
					}
				})
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			files, err := r.ReadTree(ctx, ref, "")
			if err != nil || len(files) != writers {
				t.Fatalf("tree has %d files (%v)", len(files), err)
			}
			if n := git(t, r.Dir(), "rev-list", "--count", ref); n != fmt.Sprint(writers) {
				t.Fatalf("history has %s commits, want %d", n, writers)
			}
			t.Logf("%d CAS losses retried", losses)
		})
	}
}

func TestCheckedOutBranchRefused(t *testing.T) {
	ctx := context.Background()
	w := WriteRequest{Message: "m", Signature: testSig, Changes: Changes{Put: map[string][]byte{"x": []byte("x")}}}
	// The main work tree's branch, from the main work tree.
	r := newRepo(t, kindNormal)
	w.Ref = "refs/heads/main"
	if _, err := r.WriteRef(ctx, w); !errors.Is(err, ErrCheckedOut) {
		t.Fatalf("current branch err = %v", err)
	}
	// A branch checked out in a linked worktree, written from the main one,
	// and the main one's branch written from the linked worktree.
	r = newRepo(t, kindWorktree)
	mainDir := filepath.Join(filepath.Dir(r.Dir()), "src")
	rm, err := Open(ctx, mainDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.Ref = "refs/heads/wtbranch"
	if _, err := rm.WriteRef(ctx, w); !errors.Is(err, ErrCheckedOut) {
		t.Fatalf("linked worktree branch err = %v", err)
	}
	w.Ref = "refs/heads/main"
	if _, err := r.WriteRef(ctx, w); !errors.Is(err, ErrCheckedOut) {
		t.Fatalf("main branch from linked worktree err = %v", err)
	}
	// A bare repo's HEAD branch is not checked out: stores land there.
	rb := newRepo(t, kindBare)
	if res, err := rb.WriteRef(ctx, w); err != nil || !res.Changed {
		t.Fatalf("bare HEAD branch = %+v %v", res, err)
	}
	// A branch nobody has checked out is fine in a normal repo.
	git(t, mainDir, "branch", "other", "main")
	w.Ref = "refs/heads/other"
	if res, err := rm.WriteRef(ctx, w); err != nil || !res.Changed {
		t.Fatalf("other branch = %+v %v", res, err)
	}
}

func TestInheritedRepoEnvIsIgnored(t *testing.T) {
	// Inside a git hook, GIT_DIR and GIT_INDEX_FILE point at the hook's
	// repository. A Repo must still act on its own directory.
	ctx := context.Background()
	decoy := newRepo(t, kindNormal)
	r := newRepo(t, kindBare)
	t.Setenv("GIT_DIR", filepath.Join(decoy.Dir(), ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(decoy.Dir(), ".git", "index"))
	if _, err := r.WriteRef(ctx, WriteRequest{Ref: "refs/lore/live", Message: "m", Signature: testSig,
		Changes: Changes{Put: map[string][]byte{"x": []byte("x")}}}); err != nil {
		t.Fatal(err)
	}
	// t.Setenv restores at cleanup; the check below needs them gone now.
	t.Setenv("GIT_DIR", "")
	t.Setenv("GIT_INDEX_FILE", "")
	if out := git(t, decoy.Dir(), "for-each-ref", "refs/lore/"); out != "" {
		t.Fatalf("write landed in the decoy: %s", out)
	}
}

func TestAncestry(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t, kindBare)
	a, _ := r.WriteRef(ctx, WriteRequest{Ref: "refs/lore/live", Message: "a", Signature: testSig, Changes: Changes{Put: map[string][]byte{"a": nil}}})
	b, _ := r.WriteRef(ctx, WriteRequest{Ref: "refs/lore/live", Message: "b", Signature: testSig, Changes: Changes{Put: map[string][]byte{"b": nil}}})
	orphan, _ := r.WriteRef(ctx, WriteRequest{Ref: "refs/lore/signals", History: HistoryReplace, Message: "s", Signature: testSig, Changes: Changes{Put: map[string][]byte{"s": nil}}})
	if ok, err := r.IsAncestor(ctx, a.New, b.New); err != nil || !ok {
		t.Fatalf("a<b = %v %v", ok, err)
	}
	if ok, err := r.IsAncestor(ctx, b.New, a.New); err != nil || ok {
		t.Fatalf("b<a = %v %v", ok, err)
	}
	if mb, ok, err := r.MergeBase(ctx, a.New, b.New); err != nil || !ok || mb != a.New {
		t.Fatalf("merge-base = %v %v %v", mb, ok, err)
	}
	if _, ok, err := r.MergeBase(ctx, b.New, orphan.New); err != nil || ok {
		t.Fatalf("unrelated merge-base = %v %v", ok, err)
	}
}

func TestMergeParents(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t, kindBare)
	a, _ := r.WriteRef(ctx, WriteRequest{Ref: "refs/lore/live", Message: "a", Signature: testSig, Changes: Changes{Put: map[string][]byte{"a": []byte("a")}}})
	b, _ := r.WriteRef(ctx, WriteRequest{Ref: "refs/lore/other", Message: "b", Signature: testSig, Changes: Changes{Put: map[string][]byte{"b": []byte("b")}}})
	m, err := r.MakeCommit(ctx, CommitRequest{Base: a.New, MergeParents: []string{"refs/lore/other"}, Message: "merge", Signature: testSig,
		Changes: Changes{Put: map[string][]byte{"b": []byte("b")}}})
	if err != nil || !m.Changed {
		t.Fatalf("merge = %+v %v", m, err)
	}
	if got := parents(t, r, m.OID); !slices.Equal(got, []string{a.New, b.New}) {
		t.Fatalf("parents = %v", got)
	}
	// Same tree as base: still a merge commit (records the other line).
	m2, err := r.MakeCommit(ctx, CommitRequest{Base: a.New, MergeParents: []string{b.New}, Message: "merge", Signature: testSig})
	if err != nil || !m2.Changed || len(parents(t, r, m2.OID)) != 2 {
		t.Fatalf("tree-equal merge = %+v %v", m2, err)
	}
	for _, bad := range []CommitRequest{
		{MergeParents: []string{b.New}, Signature: testSig},
		{Base: a.New, History: HistoryReplace, MergeParents: []string{b.New}, Signature: testSig},
		{Base: a.New, MergeParents: []string{"refs/lore/missing"}, Signature: testSig},
	} {
		if _, err := r.MakeCommit(ctx, bad); !errors.Is(err, ErrBadParents) {
			t.Errorf("%+v: err = %v", bad, err)
		}
	}
}
