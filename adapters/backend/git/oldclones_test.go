package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// oldStore is a remote seeded in the layout of an earlier 0.1.18 build.
type oldStore struct {
	remote string            // the bare remote
	rows   map[string][]byte // root-relative path → bytes, as seeded
	ids    []ids.AnyID       // the seeded row ids, in order
}

// oldMarker is a lore-store.yaml of an earlier build: lore_store 2, a
// store id, and org_folder as given ("" none).
func oldMarker(t *testing.T, orgFolder string) []byte {
	t.Helper()
	id, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	s := "lore_store: 2\nid: " + id.String() + "\nvisibility: internal\ncreated: \"2026-11-01\"\n"
	if orgFolder != "" {
		s += "org_folder: " + orgFolder + "\n"
	}
	return []byte(s)
}

// newOldRemote seeds a bare remote with an old-layout store: the marker
// (org_folder as given), one memory row in each of folders, and the
// extra files given (root-relative path → bytes).
func newOldRemote(t *testing.T, orgFolder string, folders []string, extra map[string][]byte) oldStore {
	t.Helper()
	dir := t.TempDir()
	o := oldStore{remote: filepath.Join(dir, "remote.git"), rows: map[string][]byte{}}
	gitT(t, dir, "init", "--quiet", "--bare", "--initial-branch="+testBranch, o.remote)
	seed := filepath.Join(dir, "seed")
	gitT(t, dir, "init", "--quiet", "--initial-branch="+testBranch, seed)
	files := map[string][]byte{rowfs.MarkerFile: oldMarker(t, orgFolder)}
	g := newGen(t)
	for _, f := range folders {
		id := newID(t)
		r := g.row(id, "note in "+f, hlc.HLC{})
		p := rowfs.LayoutRepo.StorePath(f, rowfs.RowPath(memories, id))
		files[p] = r.Doc
		o.rows[p] = r.Doc
		o.ids = append(o.ids, id)
	}
	for p, b := range extra {
		files[p] = b
	}
	for p, b := range files {
		full := filepath.Join(seed, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, seed, "add", "-A")
	gitT(t, seed, "commit", "--quiet", "-m", "old layout")
	gitT(t, seed, "push", "--quiet", o.remote, testBranch)
	return o
}

// makeOldClone builds an old clone of remote under home the way the earlier
// build did: a bare clone at clones/<rel>.git whose origin is origin, its
// tracking branches fetched from remote, and a linked worktree at
// clones/<rel>/work on branch main, plus the clone lock file.
func makeOldClone(t *testing.T, home, rel, origin, remote string) OldClone {
	t.Helper()
	root := filepath.Join(home, OldClonesDir)
	base := filepath.Join(root, filepath.FromSlash(rel))
	o := oldClone(root, base)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	gitT(t, filepath.Dir(base), "init", "--quiet", "--bare", "--", o.Bare)
	gitT(t, o.Bare, "remote", "add", "origin", origin)
	gitT(t, o.Bare, "fetch", "--quiet", remote, "+refs/heads/*:refs/remotes/origin/*")
	gitT(t, o.Bare, "worktree", "add", "--quiet", "-b", testBranch, "--", o.Work, "refs/remotes/origin/"+testBranch)
	if err := os.WriteFile(o.Lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// the old worktree's own lore state, never carried
	writeFile(t, filepath.Join(o.Work, ".lore", "lock"), "")
	return oldClone(root, base)
}

// hintOf is a lore error's hint ("" for another error).
func hintOf(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestMoveOldClone (brief 1.6): an old clone with a commit the remote
// lacks, an edited row and a new file lands at stores/<owner>/<repo>
// with all three; the old clone stays; a second run does nothing; the
// record says it may be removed, until the old worktree changes.
func TestMoveOldClone(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	src := newOldRemote(t, "", []string{"team", "."}, nil)
	const origin = "git@example.com:acme/notes.git"
	o := makeOldClone(t, home, "example.com/acme/notes", origin, src.remote)
	writeFile(t, filepath.Join(o.Work, "team", ".lore", "data", "memories", "pending.json"), "{}\n")
	gitT(t, o.Work, "add", "-A", "team")
	gitT(t, o.Work, "commit", "--quiet", "-m", "pending")
	var edited string
	for p := range src.rows {
		edited = p
		break
	}
	writeFile(t, filepath.Join(o.Work, filepath.FromSlash(edited)), "edited\n")
	writeFile(t, filepath.Join(o.Work, "notes.txt"), "a hand note\n")
	if !MoveNeeded(home) {
		t.Fatal("MoveNeeded is false with an old clone not moved")
	}
	lock, err := MoveLock(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	res, err := MoveOldClone(ctx, home, o, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, StoresDir, "acme", "notes")
	if res.To != want || res.Branch != testBranch || res.Carried != 1 || len(res.Files) != 2 || res.Already {
		t.Fatalf("move: %+v, want to %s, 1 commit and 2 files carried", res, want)
	}
	if got := gitT(t, want, "rev-parse", "HEAD"); got != WorktreeTip(o) {
		t.Fatalf("new clone at %s, old tip %s", got, WorktreeTip(o))
	}
	if got := OriginAt(want); got != origin {
		t.Fatalf("origin %q, want the old spelling %q", got, origin)
	}
	if readFile(t, filepath.Join(want, filepath.FromSlash(edited))) != "edited\n" || readFile(t, filepath.Join(want, "notes.txt")) != "a hand note\n" {
		t.Fatal("uncommitted changes not carried")
	}
	if _, err := os.Stat(filepath.Join(want, ".lore", "lock")); err == nil {
		t.Fatal("the old worktree's .lore/lock was carried")
	}
	if up := gitT(t, want, "config", "branch."+testBranch+".remote"); up != RemoteName {
		t.Fatalf("branch upstream %q", up)
	}
	if !isDir(o.Bare) || !isDir(o.Work) {
		t.Fatal("the old clone was removed")
	}
	if MoveNeeded(home) {
		t.Fatal("MoveNeeded after the move")
	}
	again, err := MoveOldClone(ctx, home, o, time.Now())
	if err != nil || !again.Already {
		t.Fatalf("second move: %+v %v", again, err)
	}
	if _, ok, why := MovedSafely(ctx, home, o); !ok {
		t.Fatalf("MovedSafely after the move: %s", why)
	}
	writeFile(t, filepath.Join(o.Work, "later.txt"), "written after the move\n")
	if _, ok, _ := MovedSafely(ctx, home, o); ok {
		t.Fatal("MovedSafely with an old worktree changed after the move")
	}
	if !strings.Contains(o.RemoveCommand(), o.Bare) || !strings.HasPrefix(o.RemoveCommand(), "rm -rf "+o.Base) {
		t.Fatalf("remove command %q", o.RemoveCommand())
	}
}

// TestMoveOldCloneAlias: an origin through an ssh alias lands at the url's
// path (never under the alias, never a "~2"), and opening the store by its
// canonical url later finds that same clone.
func TestMoveOldCloneAlias(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	src := newOldRemote(t, "", []string{"team"}, nil)
	o := makeOldClone(t, home, "work-gh/acme/notes", "git@work-gh:acme/notes.git", src.remote)
	res, err := MoveOldClone(ctx, home, o, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, StoresDir, "acme", "notes")
	if res.To != want {
		t.Fatalf("alias clone moved to %s, want %s", res.To, want)
	}
	canon, _, err := CloneDirOf(home, "git@github.com:acme/notes.git", "")
	if err != nil || canon != want {
		t.Fatalf("the canonical url opens %s (%v), want the moved clone %s", canon, err, want)
	}
}

// TestSameRepoViaAliasAsksSSHConfig: with an ssh-config resolver
// installed, what ssh connects to decides whether two host names are one
// store's: an alias with a dot in it is recognised, an alias sent to a
// third host is not the same store although it has no dot, two aliases of
// one host are one store, and only a host the config does not name falls
// back to the no-dot guess. Not parallel: the resolver is process-wide.
func TestSameRepoViaAliasAsksSSHConfig(t *testing.T) {
	remote := func(raw string) Remote {
		t.Helper()
		r, err := RemoteOfRaw(raw)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	canon := remote("git@github.com:acme/notes.git")
	cases := []struct {
		name, other string
		want, guess bool // guess: the answer without an ssh config
	}{
		{"alias renamed to the host", "git@work-gh:acme/notes.git", true, true},
		{"dotted alias renamed to the host", "git@gh.work.example:acme/notes.git", true, false},
		{"alias sent to another host", "git@gl-work:acme/notes.git", false, true},
		{"second alias of the same host", "git@home-gh:acme/notes.git", true, true},
		{"host the config does not name", "git@corp-git:acme/notes.git", true, true},
		{"other path", "git@work-gh:acme/other.git", false, false},
	}
	for _, c := range cases {
		if got := SameRepoViaAlias(remote(c.other), canon); got != c.guess {
			t.Errorf("no ssh config, %s: %v, want %v", c.name, got, c.guess)
		}
	}
	SetAliasHosts(func(host string) (string, bool) {
		switch host {
		case "work-gh", "home-gh", "gh.work.example":
			return "github.com", true
		case "gl-work":
			return "gitlab.com", true
		}
		return "", false
	})
	t.Cleanup(func() { SetAliasHosts(nil) })
	for _, c := range cases {
		if got := SameRepoViaAlias(remote(c.other), canon); got != c.want {
			t.Errorf("ssh config, %s: %v, want %v", c.name, got, c.want)
		}
		if got := SameRepoViaAlias(canon, remote(c.other)); got != c.want {
			t.Errorf("ssh config, %s (swapped): %v, want %v", c.name, got, c.want)
		}
	}
	if !SameRepoViaAlias(remote("git@work-gh:acme/notes.git"), remote("git@home-gh:acme/notes.git")) {
		t.Error("two aliases of github.com are not one store")
	}
}

// TestMoveOldClonePinnedBranch: a pinned-branch clone (<repo>@<branch>)
// lands at <repo>@<branch>.
func TestMoveOldClonePinnedBranch(t *testing.T) {
	home := t.TempDir()
	src := newOldRemote(t, "", []string{"team"}, nil)
	o := makeOldClone(t, home, "example.com/acme/notes@staging", "git@example.com:acme/notes.git", src.remote)
	if o.Pinned != "staging" {
		t.Fatalf("pinned %q", o.Pinned)
	}
	res, err := MoveOldClone(context.Background(), home, o, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, StoresDir, "acme", "notes@staging"); res.To != want {
		t.Fatalf("moved to %s, want %s", res.To, want)
	}
}

// TestMoveOldCloneRefusesMidMerge: a worktree in the middle of a merge is
// refused with the reason and the step, and nothing is made.
func TestMoveOldCloneRefusesMidMerge(t *testing.T) {
	home := t.TempDir()
	src := newOldRemote(t, "", []string{"team"}, nil)
	o := makeOldClone(t, home, "example.com/acme/notes", "git@example.com:acme/notes.git", src.remote)
	gitdir := gitT(t, o.Work, "rev-parse", "--absolute-git-dir")
	writeFile(t, filepath.Join(gitdir, "MERGE_HEAD"), gitT(t, o.Work, "rev-parse", "HEAD")+"\n")
	_, err := MoveOldClone(context.Background(), home, o, time.Now())
	if err == nil || !strings.Contains(err.Error(), "middle of a merge") || !strings.Contains(hintOf(err), "merge --abort") {
		t.Fatalf("mid-merge move: %v (hint %q)", err, hintOf(err))
	}
	if _, serr := os.Stat(filepath.Join(home, StoresDir)); serr == nil {
		t.Fatal("a refused move made a stores folder")
	}
}

// TestMoveOldCloneResumes: a run cut short leaves a half-built folder or
// a whole clone without its record; the next run finishes either.
func TestMoveOldCloneResumes(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	src := newOldRemote(t, "", []string{"team"}, nil)
	o := makeOldClone(t, home, "example.com/acme/notes", "git@example.com:acme/notes.git", src.remote)
	writeFile(t, filepath.Join(o.Work, "notes.txt"), "a hand note\n")
	for p := range src.rows {
		writeFile(t, filepath.Join(o.Work, filepath.FromSlash(p)), "edited by hand\n")
	}
	dest := filepath.Join(home, StoresDir, "acme", "notes")
	writeFile(t, filepath.Join(dest+movingSuffix, "half"), "cut short\n")
	if _, err := MoveOldClone(ctx, home, o, time.Now()); err != nil {
		t.Fatal(err)
	}
	if isDir(dest + movingSuffix) {
		t.Fatal("the half-built folder is left")
	}
	// the record lost (cut short after the rename): the next run finds
	// the clone there, with the carried change, and records it
	if err := os.Remove(MoveRecordPath(home)); err != nil {
		t.Fatal(err)
	}
	res, err := MoveOldClone(ctx, home, o, time.Now())
	if err != nil || !res.Into || res.To != dest {
		t.Fatalf("resumed move: %+v %v", res, err)
	}
	if _, ok, why := MovedSafely(ctx, home, o); !ok {
		t.Fatalf("not safe after the resumed move: %s", why)
	}
	// the same path changed another way in the new clone: refused
	for p := range src.rows {
		writeFile(t, filepath.Join(dest, filepath.FromSlash(p)), "edited in the new clone\n")
	}
	writeFile(t, filepath.Join(o.Work, "later.txt"), "committed later\n")
	gitT(t, o.Work, "add", "later.txt")
	gitT(t, o.Work, "commit", "--quiet", "-m", "later")
	if _, err := MoveOldClone(ctx, home, o, time.Now()); err == nil || !strings.Contains(err.Error(), "in both the old clone") {
		t.Fatalf("two edits of one file: %v", err)
	}
}

// TestMoveOldCloneIntoExisting: a newer lore cloned the store already;
// the old clone's unpushed commit is brought in by fast-forward.
func TestMoveOldCloneIntoExisting(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	src := newOldRemote(t, "", []string{"team"}, nil)
	o := makeOldClone(t, home, "example.com/acme/notes", "git@example.com:acme/notes.git", src.remote)
	writeFile(t, filepath.Join(o.Work, "team", ".lore", "data", "memories", "pending.json"), "{}\n")
	gitT(t, o.Work, "add", "-A", "team")
	gitT(t, o.Work, "commit", "--quiet", "-m", "pending")
	dest := filepath.Join(home, StoresDir, "acme", "notes")
	b, err := OpenClone(ctx, src.remote, dest, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	res, err := MoveOldClone(ctx, home, o, time.Now())
	if err != nil || !res.Into || res.Carried != 1 {
		t.Fatalf("move into the existing clone: %+v %v", res, err)
	}
	if got := gitT(t, dest, "rev-parse", "HEAD"); got != WorktreeTip(o) {
		t.Fatalf("existing clone at %s, want the old tip %s", got, WorktreeTip(o))
	}
}

// TestScanOldOrgClones lists the org link clones (reported, never moved).
func TestScanOldOrgClones(t *testing.T) {
	home := t.TempDir()
	bare := filepath.Join(home, OldOrgClonesDir, "example.com", "acme", "notes.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o700); err != nil {
		t.Fatal(err)
	}
	gitT(t, filepath.Dir(bare), "init", "--quiet", "--bare", "--", bare)
	got, err := ScanOldOrgClones(home)
	if err != nil || len(got) != 1 || got[0] != bare {
		t.Fatalf("org clones %v %v", got, err)
	}
	if found, _ := ScanOldClones(home); len(found) != 0 {
		t.Fatalf("org clones listed as store clones: %v", found)
	}
}

// TestLoreBookkeeping: an old worktree's own lore files are never carried;
// rows and the org config are.
func TestLoreBookkeeping(t *testing.T) {
	for p, want := range map[string]bool{
		".lore/lock":                      true,
		".lore/state/journal":             true,
		"team/.lore/lock":                 true,
		".lore-state/lock":                true,
		".lore/data/memories/x.json":      false,
		"team/.lore/data/memories/x.json": false,
		"org/.lore/lore-org.yaml":         false,
		"notes.txt":                       false,
	} {
		if got := loreBookkeeping(p); got != want {
			t.Errorf("loreBookkeeping(%q) = %v, want %v", p, got, want)
		}
	}
}
