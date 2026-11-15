package gitfast

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// gitT runs real git in dir and fails the test on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, code := realGit(t, dir, args, nil)
	if code != 0 {
		t.Fatalf("git %v: exit %d: %s", args, code, out)
	}
	return strings.TrimSpace(string(out))
}

// realGit runs git and returns stdout and the exit status.
func realGit(t *testing.T, dir string, args []string, stdin []byte) ([]byte, int) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Ann", "GIT_AUTHOR_EMAIL=ann@example.test",
		"GIT_COMMITTER_NAME=Ann", "GIT_COMMITTER_EMAIL=ann@example.test", "HOME="+t.TempDir())
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out, ee.ExitCode()
	}
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out, 0
}

// fixture builds a repository with loose and packed objects (deltas),
// packed and loose refs, a symref, an annotated tag, nested trees, a
// linked worktree and a bare clone. It returns the work tree, the linked
// worktree and the bare clone.
func fixture(t *testing.T) (work, linked, bare string) {
	t.Helper()
	root := t.TempDir()
	work = filepath.Join(root, "work")
	gitT(t, root, "init", "-q", "-b", "main", work)
	write := func(p, body string) {
		full := filepath.Join(work, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	big := strings.Repeat("line of text that deltas well\n", 200)
	for i := range 6 {
		write("org/tasks/t"+strconv.Itoa(i)+".json", big+strconv.Itoa(i))
		write("org/actors/a.json", big+"actor "+strconv.Itoa(i))
		if i < 3 {
			write("_store/meta.json", "{}"+strconv.Itoa(i%2))
		}
		write("top.txt", "top "+strconv.Itoa(i))
		if i == 3 {
			if err := os.Remove(filepath.Join(work, "_store", "meta.json")); err != nil {
				t.Fatal(err)
			}
			write("_store/meta.json/inner", "dir now")
		}
		gitT(t, work, "add", "-A")
		gitT(t, work, "commit", "-q", "-m", "c"+strconv.Itoa(i))
		if i == 2 {
			gitT(t, work, "tag", "-a", "v1", "-m", "tag")
			gitT(t, work, "gc", "-q", "--aggressive") // packs with deltas, packed-refs
		}
	}
	gitT(t, work, "update-ref", "refs/lore/live/main", "HEAD~1")
	gitT(t, work, "update-ref", "refs/remotes/origin/main", "HEAD~2")
	gitT(t, work, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	gitT(t, work, "branch", "side", "HEAD~3")
	linked = filepath.Join(root, "linked")
	gitT(t, work, "worktree", "add", "-q", linked, "side")
	bare = filepath.Join(root, "bare.git")
	gitT(t, root, "clone", "-q", "--bare", work, bare)
	gitT(t, linked, "update-index", "--index-version", "4") // prefix-compressed paths
	write("org/tasks/new.json", "staged, not committed")
	gitT(t, work, "add", "org/tasks/new.json")
	return work, linked, bare
}

// TestAnswersMatchGit runs every invocation shape the Session answers both
// ways and requires the same bytes and exit status (devco:D-418: the
// in-process reads must be exactly git's).
func TestAnswersMatchGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	work, linked, bare := fixture(t)
	head := gitT(t, work, "rev-parse", "HEAD")
	first := gitT(t, work, "rev-list", "--max-parents=0", "HEAD")
	tree := gitT(t, work, "rev-parse", "HEAD^{tree}")
	side := gitT(t, work, "rev-parse", "side")
	tables := []string{"--", "org/tasks", "org/actors", "_store/meta.json", "missing"}
	cases := []struct {
		dir   string
		args  []string
		stdin string
	}{
		{work, []string{"rev-parse", "--verify", "--quiet", "refs/lore/live/main^{commit}"}, ""},
		{work, []string{"rev-parse", "--verify", "--quiet", "refs/lore/nothing^{commit}"}, ""},
		{work, []string{"rev-parse", "--verify", "--quiet", "--end-of-options", "HEAD^{commit}"}, ""},
		{work, []string{"rev-parse", "--verify", "--quiet", "v1^{commit}"}, ""},
		{work, []string{"rev-parse", "--verify", "--quiet", "v1"}, ""},
		{work, []string{"rev-parse", "--verify", "--quiet", "main"}, ""},
		{work, []string{"rev-parse", "--verify", "--quiet", "origin/main^{commit}"}, ""},
		{work, []string{"rev-parse", "--verify", "--quiet", head + "^{commit}"}, ""},
		{work, []string{"rev-parse", "--verify", "--quiet", tree + "^{commit}"}, ""},
		{work, []string{"rev-parse", "--verify", "--quiet", strings.Repeat("1", 40) + "^{commit}"}, ""},
		{work, []string{"rev-parse", head + ":org/actors/a.json"}, ""},
		{work, []string{"rev-parse", "--show-toplevel"}, ""},
		{filepath.Join(work, "org", "tasks"), []string{"rev-parse", "--show-prefix"}, ""},
		{work, []string{"rev-parse", "--show-prefix"}, ""},
		{work, []string{"rev-parse", "--absolute-git-dir"}, ""},
		{linked, []string{"rev-parse", "--absolute-git-dir"}, ""},
		{linked, []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}, ""},
		{linked, []string{"rev-parse", "--path-format=absolute", "--git-path", "index", "--git-path", "config", "--git-path", "info/attributes"}, ""},
		{work, []string{"rev-parse", "--path-format=absolute", "--git-path", "index.lock", "--git-path", "lore/x"}, ""},
		{work, []string{"rev-parse", "--is-inside-work-tree"}, ""},
		{work, []string{"rev-parse", "--path-format=absolute", "--git-path", "hooks"}, ""},
		{linked, []string{"rev-parse", "--path-format=absolute", "--git-path", "hooks", "--git-path", "index"}, ""},
		{bare, []string{"rev-parse", "--is-bare-repository", "--show-object-format"}, ""},
		{work, []string{"rev-parse", "--is-bare-repository", "--show-object-format"}, ""},
		{work, []string{"symbolic-ref", "--short", "HEAD"}, ""},
		{linked, []string{"symbolic-ref", "--quiet", "--short", "HEAD"}, ""},
		{work, []string{"symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"}, ""},
		{work, []string{"symbolic-ref", "--quiet", "refs/heads/main"}, ""},
		{work, []string{"symbolic-ref", "--quiet", "refs/heads/none"}, ""},
		{work, []string{"for-each-ref", "--format=%(objectname) %(refname)", "--", "refs/lore/live"}, ""},
		{work, []string{"for-each-ref", "--format=%(objectname) %(refname)", "refs/remotes/"}, ""},
		{work, []string{"for-each-ref", "--count=1", "--format=%(refname)", "refs/heads/", "refs/remotes/"}, ""},
		{work, []string{"for-each-ref", "--format=%(refname)%00%(objecttype)"}, ""},
		{bare, []string{"for-each-ref"}, ""},
		{work, []string{"check-ref-format", "refs/lore/live"}, ""},
		{work, []string{"check-ref-format", "refs/lore/a..b"}, ""},
		{work, []string{"check-ref-format", "refs/x.lock/y"}, ""},
		{work, []string{"check-ref-format", "main"}, ""},
		{work, []string{"check-ref-format", "refs/a b"}, ""},
		{work, []string{"cat-file", "blob", head + ":org/actors/a.json"}, ""},
		{work, []string{"cat-file", "-e", head + ":top.txt"}, ""},
		{work, []string{"cat-file", "-t", "v1"}, ""},
		{work, []string{"cat-file", "--batch"}, head + "\n" + tree + "\n" + strings.Repeat("2", 40) + "\n" + first + ":top.txt\n"},
		{work, []string{"cat-file", "--batch-check"}, head + "\nv1\n"},
		{work, []string{"ls-tree", "-r", "-z", "--name-only", head}, ""},
		{work, append([]string{"ls-tree", "-r", "-z", "--name-only", head}, tables...), ""},
		{work, []string{"ls-tree", "-r", "-z", "--full-tree", "refs/lore/live/main", "--", "org/tasks"}, ""},
		{work, []string{"ls-tree", "-r", head, "--", "org/"}, ""},
		{work, []string{"ls-tree", head}, ""},
		{work, []string{"ls-tree", "--name-only", head, "--", "org/"}, ""},
		{work, []string{"--literal-pathspecs", "ls-tree", "-r", "-z", "--full-tree", head, "--", "org/actors", "top.txt"}, ""},
		{work, append([]string{"diff", "--name-status", "-z", "--no-renames", first, head}, tables...), ""},
		{work, []string{"diff", "--name-status", "-z", "--no-renames", head, head}, ""},
		{work, []string{"diff", "--name-status", "-z", "--no-renames", side, head}, ""},
		{work, []string{"diff", "--name-only", "--no-renames", first, head}, ""},
		{work, []string{"diff-tree", "-r", "-z", "--no-renames", "--name-only", first, head}, ""},
		{work, []string{"diff-tree", "-r", "-z", "--name-status", side, head, "--", "org"}, ""},
		{work, []string{"rev-list", "--count", head, "^" + side}, ""},
		{work, []string{"rev-list", "--count", side, "^" + head}, ""},
		{work, []string{"merge-base", "--is-ancestor", side, head}, ""},
		{work, []string{"merge-base", "--is-ancestor", head, side}, ""},
		{work, []string{"merge-base", head, side}, ""},
		{work, []string{"merge-base", side, head}, ""},
		{work, []string{"ls-files", "--stage", "-z", "--", "."}, ""},
		{filepath.Join(work, "org"), []string{"ls-files", "--stage", "-z", "--", "."}, ""},
		{filepath.Join(work, "org"), []string{"ls-files", "--stage", "-z", "--", "tasks"}, ""},
		{work, []string{"ls-files", "--stage", "-z", "--", "org/tasks", "top.txt"}, ""},
		{linked, []string{"ls-files", "--stage", "-z"}, ""},
		{work, []string{"ls-files", "--error-unmatch", "--", "top.txt"}, ""},
		{filepath.Join(work, "org"), []string{"ls-files", "-z", "--", "tasks", "nope"}, ""},
		{filepath.Join(work, "org"), []string{"ls-files", "--full-name", "--", "."}, ""},
		{work, []string{"ls-files"}, ""},
		{work, []string{"rev-parse", "--git-dir"}, ""},
		{filepath.Join(work, "org"), []string{"rev-parse", "--git-dir"}, ""},
		{linked, []string{"rev-parse", "--git-dir"}, ""},
		{work, []string{"cat-file", "--batch"}, ":org/tasks/new.json\n:nope\n:0:top.txt\n" + head + ":nope\n"},
		{work, []string{"rev-parse", "--verify", "--quiet", "HEAD~1^{commit}"}, ""},
		{work, []string{"rev-parse", "--verify", "--quiet", "HEAD~3"}, ""},
		{work, []string{"rev-parse", "--verify", "--quiet", "main^^"}, ""},
		{work, []string{"rev-parse", "--verify", "--quiet", "HEAD~40"}, ""},
		{work, []string{"merge-base", "HEAD~1", "HEAD"}, ""},
		{work, []string{"log", "-1", "--format=%B", "HEAD"}, ""},
		{work, []string{"log", "-1", "--format=%ct%x09%an", "refs/heads/main", "--"}, ""},
		{work, []string{"log", "-n", "1", "--format=%H %T %P%n%ae %ce %at", "side"}, ""},
		{work, []string{"rev-list", "-1", "HEAD", "--not", "--remotes"}, ""},
		{work, []string{"rev-list", "-1", "HEAD~2", "--not", "--remotes"}, ""},
		{work, []string{"diff", "--cached", "--quiet", "HEAD", "--"}, ""},
		{work, []string{"diff", "--cached", "--quiet", "HEAD", "--", "top.txt"}, ""},
		{work, []string{"diff", "--cached", "--quiet", "HEAD", "--", "org"}, ""},
		{work, []string{"-c", "commit.gpgsign=false", "-c", "core.autocrlf=false", "rev-parse", "--verify", "--quiet", "refs/heads/main^{commit}"}, ""},
	}
	for _, c := range cases {
		s := New()
		var stdin []byte
		if c.stdin != "" {
			stdin = []byte(c.stdin)
		}
		rep, ok := s.Answer(context.Background(), c.dir, c.args, nil, stdin)
		if !ok {
			t.Errorf("not answered in-process (want it answered): git %v", c.args)
			continue
		}
		out, code := realGit(t, c.dir, c.args, stdin)
		if !bytes.Equal(rep.Out, out) || rep.Code != code {
			t.Errorf("git %v\n gitfast: exit %d %q\n git:     exit %d %q", c.args, rep.Code, rep.Out, code, out)
		}
		s.Close()
	}
}

// TestFallsBack checks the shapes left to git are not answered.
func TestFallsBack(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	work, _, _ := fixture(t)
	s := New()
	defer s.Close()
	for _, args := range [][]string{
		{"rev-parse", "--verify", "HEAD^2"},
		{"log", "-2", "--format=%H", "HEAD"},
		{"rev-list", "--boundary", "HEAD", "--not", "--remotes"},
		{"rev-parse", "--git-path", "hooks"},   // relative form
		{"rev-parse", "--verify", "refs/none"}, // git prints the reason
		{"status", "--porcelain"},
		{"ls-files", "--error-unmatch", "--", "nope"}, // git words the miss
		{"log", "-1"},
		{"ls-tree", "HEAD", "--", "org"},
		{"-c", "user.name=x", "rev-parse", "HEAD"},
		{"for-each-ref", "--sort=-refname"},
	} {
		if _, ok := s.Answer(context.Background(), work, args, nil, nil); ok {
			t.Errorf("git %v was answered in-process; it must run git", args)
		}
	}
	if _, ok := s.Answer(context.Background(), work, []string{"rev-parse", "HEAD"}, []string{"GIT_DIR=x"}, nil); ok {
		t.Error("an invocation with GIT_DIR was answered in-process")
	}
	if _, ok := s.Answer(context.Background(), work, []string{"ls-files", "--stage", "-z"}, []string{"GIT_INDEX_FILE=x"}, nil); ok {
		t.Error("an index read with another GIT_INDEX_FILE was answered in-process")
	}
	for _, args := range [][]string{{"diff", "--cached", "--quiet", "HEAD", "--"}, {"cat-file", "--batch"}, {"diff", "--name-only", "HEAD", "HEAD"}} {
		if _, ok := s.Answer(context.Background(), work, args, []string{"GIT_INDEX_FILE=.git/index.lock"}, []byte(":top.txt\n")); ok {
			t.Errorf("git %v under a temporary index (commit -a hooks) was answered from the usual index", args)
		}
	}
	if rep, ok := s.Answer(context.Background(), work, []string{"diff", "--cached", "--quiet", "HEAD", "--"}, []string{"GIT_INDEX_FILE=" + filepath.Join(work, ".git", "index")}, nil); !ok || rep.Code != 1 {
		t.Errorf("diff --cached under the usual index named in GIT_INDEX_FILE: answered %v %+v, want exit 1 (a file is staged)", ok, rep)
	}
	if rep, ok := s.Answer(context.Background(), work, []string{"rev-parse", "--verify", "--quiet", "HEAD^{commit}"}, []string{"GIT_INDEX_FILE=x", "GIT_AUTHOR_NAME=y"}, nil); !ok || rep.Code != 0 {
		t.Error("a ref read under an index file and an identity was not answered")
	}
}

// TestRefsAreNeverCached: a ref moved by another process between two
// questions is seen at once (refs are read from the files every time).
func TestRefsAreNeverCached(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	work, _, _ := fixture(t)
	s := New()
	defer s.Close()
	ask := func() string {
		rep, ok := s.Answer(context.Background(), work, []string{"rev-parse", "--verify", "--quiet", "refs/lore/live/main^{commit}"}, nil, nil)
		if !ok {
			t.Fatal("not answered")
		}
		return string(rep.Out)
	}
	before := ask()
	gitT(t, work, "update-ref", "refs/lore/live/main", "HEAD")
	after := ask()
	if before == after || strings.TrimSpace(after) != gitT(t, work, "rev-parse", "HEAD") {
		t.Fatalf("ref move not seen: before %q after %q", before, after)
	}
}

// TestConfigSnapshotDroppedOnWrite: a config write through any runner
// drops the snapshot, so the next read sees the new value.
func TestConfigSnapshotDroppedOnWrite(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	work, _, _ := fixture(t)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
	s := New()
	defer s.Close()
	ctx := context.Background()
	get := func() Reply {
		rep, ok := s.Answer(ctx, work, []string{"config", "--get", "remote.origin.url"}, nil, nil)
		if !ok {
			t.Fatal("config read not answered")
		}
		return rep
	}
	if rep := get(); rep.Code != 1 {
		t.Fatalf("unset key: %+v", rep)
	}
	args := []string{"config", "remote.origin.url", "file:///x"}
	if _, ok := s.Answer(ctx, work, args, nil, nil); ok {
		t.Fatal("a config write was answered")
	}
	out, code := realGit(t, work, args, nil)
	s.Record(work, args, nil, nil, out, code, true)
	if rep := get(); rep.Code != 0 || string(rep.Out) != "file:///x\n" {
		t.Fatalf("after write: %+v", rep)
	}
	// the implicit get (`git config <key>`) answers the same
	if rep, ok := s.Answer(ctx, work, []string{"config", "remote.origin.url"}, nil, nil); !ok || string(rep.Out) != "file:///x\n" {
		t.Fatalf("implicit get: %v %+v", ok, rep)
	}
}

// TestPersistedAcrossSessions: a content-addressed log answer is kept in
// the lore home's cache folder for the next process (never inside the
// repository), and a change to a config file (part of the key) makes the
// next one ask git again.
func TestPersistedAcrossSessions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	work, _, _ := fixture(t)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
	cache := t.TempDir()
	head := gitT(t, work, "rev-parse", "HEAD")
	args := []string{"log", "--reverse", "--format=%H %s", head, "--"}
	ctx := context.Background()
	newS := func() *Session {
		s := New()
		s.SetCacheDir(cache)
		return s
	}
	s1 := newS()
	if _, ok := s1.Answer(ctx, work, args, nil, nil); ok {
		t.Fatal("answered before anything was kept")
	}
	out, code := realGit(t, work, args, nil)
	s1.Record(work, args, nil, nil, out, code, true)
	s1.Close()
	s2 := newS()
	rep, ok := s2.Answer(ctx, work, args, nil, nil)
	if !ok || !bytes.Equal(rep.Out, out) || rep.Code != 0 {
		t.Fatalf("second process: answered %v, %q, want %q", ok, rep.Out, out)
	}
	s2.Close()
	if _, err := os.Stat(filepath.Join(work, ".git", "lore")); !os.IsNotExist(err) {
		t.Fatalf("the cache wrote inside the repository: %v", err)
	}
	// a config change (size and time of .git/config move) is a new key
	gitT(t, work, "config", "log.showSignature", "false")
	s3 := newS()
	defer s3.Close()
	if _, ok := s3.Answer(ctx, work, args, nil, nil); ok {
		t.Fatal("answered from the cache after the repository config changed")
	}
	// without a cache folder nothing is kept across processes
	s4 := New()
	s4.Record(work, args, nil, nil, out, code, true)
	s4.Close()
	if s5 := New(); func() bool { _, ok := s5.Answer(ctx, work, args, nil, nil); return ok }() {
		t.Fatal("a Session without a cache folder answered from a previous one")
	}
}

// TestConfigPersistedAcrossSessions: the second process reads a folder's
// config without git; a change to the global config (or an included file)
// makes it read again.
func TestConfigPersistedAcrossSessions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	work, _, _ := fixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	inc := filepath.Join(home, "inc")
	must(t, os.WriteFile(inc, []byte("[user]\n\temail = one@example.test\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[include]\n\tpath = "+inc+"\n"), 0o644))
	cache := t.TempDir()
	ctx := context.Background()
	execs := 0
	ask := func() string {
		s := New()
		s.SetCacheDir(cache)
		inner := s.execGit
		s.git = func(ctx context.Context, dir string, args []string) ([]byte, error) {
			execs++
			s.git = nil
			defer func() { s.git = nil }()
			return inner(ctx, dir, args)
		}
		defer s.Close()
		rep, ok := s.Answer(ctx, work, []string{"config", "--get", "user.email"}, nil, nil)
		if !ok {
			t.Fatal("not answered")
		}
		return string(rep.Out)
	}
	if got := ask(); got != "one@example.test\n" || execs != 1 {
		t.Fatalf("first: %q, %d execs", got, execs)
	}
	if got := ask(); got != "one@example.test\n" || execs != 1 {
		t.Fatalf("second process ran git again or read wrong: %q, %d execs", got, execs)
	}
	must(t, os.WriteFile(inc, []byte("[user]\n\temail = two-changed@example.test\n"), 0o644))
	if got := ask(); got != "two-changed@example.test\n" || execs != 2 {
		t.Fatalf("after the included file changed: %q, %d execs", got, execs)
	}
}

// TestTemporaryIndexMatchesGit: `git commit -a` runs its hooks with
// GIT_INDEX_FILE naming a temporary index, which lore passes on as an
// absolute path. The index reads answered here read that file, byte for
// byte as git does, instead of running git for each (devco:D-420: the
// leak check of one `git commit -a` started three more git processes).
func TestTemporaryIndexMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	work, _, _ := fixture(t)
	tmp := filepath.Join(t.TempDir(), "index.tmp")
	raw, err := os.ReadFile(filepath.Join(work, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"GIT_INDEX_FILE=" + tmp}
	gitEnv := func(args []string, stdin []byte) ([]byte, int) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir(), env[0])
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		out, err := cmd.Output()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, ee.ExitCode()
		}
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out, 0
	}
	// what `commit -a` stages: a changed tracked file and a new one, in the
	// temporary index only
	if err := os.WriteFile(filepath.Join(work, "top.txt"), []byte("changed for commit -a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "org", "tasks", "only-temp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := gitEnv([]string{"add", "top.txt", "org/tasks/only-temp.json"}, nil); code != 0 {
		t.Fatal("stage into the temporary index")
	}
	s := New()
	defer s.Close()
	for _, c := range []struct {
		args  []string
		stdin []byte
	}{
		{[]string{"ls-files", "-z", "--full-name", "--", "org/tasks/"}, nil},
		{[]string{"ls-files", "--stage", "-z"}, nil},
		{[]string{"cat-file", "--batch"}, []byte(":top.txt\n:org/tasks/only-temp.json\n:org/tasks/new.json\n")},
		{[]string{"diff", "--cached", "--quiet", "HEAD", "--"}, nil},
	} {
		rep, ok := s.Answer(context.Background(), work, c.args, env, c.stdin)
		if !ok {
			t.Errorf("git %v under an absolute GIT_INDEX_FILE was not answered in-process", c.args)
			continue
		}
		want, code := gitEnv(c.args, c.stdin)
		if !bytes.Equal(rep.Out, want) || rep.Code != code {
			t.Errorf("git %v under the temporary index:\n got %d %q\nwant %d %q", c.args, rep.Code, rep.Out, code, want)
		}
	}
	// the usual index still answers from .git/index (it holds neither change)
	rep, ok := s.Answer(context.Background(), work, []string{"ls-files", "-z", "--full-name", "--", "org/tasks/"}, nil, nil)
	if !ok || bytes.Contains(rep.Out, []byte("only-temp.json")) {
		t.Errorf("the usual index answered %v %q, want no only-temp.json", ok, rep.Out)
	}
}
