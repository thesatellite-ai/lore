package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// gitRunT runs git in dir with a placeholder identity.
func gitRunT(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=placeholder", "GIT_AUTHOR_EMAIL=placeholder@example.invalid",
		"GIT_COMMITTER_NAME=placeholder", "GIT_COMMITTER_EMAIL=placeholder@example.invalid")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// makeStoreClone lays out a whole store clone at <home>/stores/<rel> the
// way the git backend does: a plain git clone (files checked out, .git
// inside), its origin the store url origin.
func makeStoreClone(t *testing.T, home, rel, origin string) gitbackend.ClonePaths {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git not installed")
	}
	seed := filepath.Join(t.TempDir(), "seed")
	gitRunT(t, t.TempDir(), "init", "-q", "--initial-branch=main", seed)
	if err := os.WriteFile(filepath.Join(seed, "placeholder.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRunT(t, seed, "add", "placeholder.txt")
	gitRunT(t, seed, "commit", "-q", "-m", "init")
	p := gitbackend.PathsOf(filepath.Join(home, gitbackend.StoresDir, filepath.FromSlash(rel)))
	if err := os.MkdirAll(filepath.Dir(p.Dir), 0o700); err != nil {
		t.Fatal(err)
	}
	gitRunT(t, seed, "clone", "-q", seed, p.Dir)
	gitRunT(t, p.Dir, "remote", "set-url", "origin", origin)
	return p
}

// TestDoctorStoreClones: a whole clone is never reported; a clone that
// lost its git data, a first clone cut short and a recorded own clone
// whose folder is gone are each named with the command that fixes it, and
// the store forget the note names removes exactly that clone. Doctor
// itself, --fix included, deletes nothing.
func TestDoctorStoreClones(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	whole := makeStoreClone(t, home, "owner/notes", "https://host.example/owner/notes")
	broken := makeStoreClone(t, home, "owner/broken", "https://host.example/owner/broken")
	cut := makeStoreClone(t, home, "owner/cut@feature", "https://host.example/owner/cut")
	if err := os.Remove(filepath.Join(broken.GitDir, "HEAD")); err != nil {
		t.Fatal(err)
	}
	gitRunT(t, cut.Dir, "commit", "-q", "--allow-empty", "-m", "waits") // a commit the remote lacks
	// cut short: HEAD still on the pending branch, nothing checked out yet
	gitRunT(t, cut.Dir, "symbolic-ref", "HEAD", "refs/heads/lore-clone-pending")
	gitRunT(t, cut.Dir, "read-tree", "--empty")
	if err := os.Remove(filepath.Join(cut.Dir, "placeholder.txt")); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(t.TempDir(), "gone")
	if err := gitbackend.RecordClone(home, "host.example/owner/mine", gitbackend.CloneEntry{Path: mine, Why: gitbackend.WhyOwn}, time.Now()); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{Home: home, RepoDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	got, err := a.DoctorStoreClones(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("findings %d, want 3 (the whole clone is not one): %+v", len(got), got)
	}
	byDir := map[string]DoctorFinding{}
	for _, f := range got {
		if f.Check != DoctorCheckStoreClone {
			t.Fatalf("check %q", f.Check)
		}
		if strings.Contains(f.Line(), whole.Dir+" ") {
			t.Fatalf("a whole clone was reported: %s", f.Line())
		}
		for _, d := range []string{broken.Dir, cut.Dir, mine} {
			if strings.HasPrefix(f.Message, d+" ") {
				byDir[d] = f
			}
		}
	}
	w := byDir[broken.Dir]
	if w.Level != DoctorWarning || !strings.Contains(w.Message, broken.Dir+" is unused") || !strings.Contains(w.Message, broken.GitDir+" is broken") || w.Fix != "rm -rf '"+broken.Dir+"'" {
		t.Fatalf("no-git-data finding: %s", w.Line())
	}
	n := byDir[cut.Dir]
	if n.Level != DoctorNote || !strings.Contains(n.Message, "cut short") || n.Fix != "lore store forget https://host.example/owner/cut" {
		t.Fatalf("not-checked-out finding: %s", n.Line())
	}
	m := byDir[mine]
	if m.Level != DoctorWarning || !strings.Contains(m.Message, "lore store use") || m.Fix != "lore store use --default https://host.example/owner/mine" {
		t.Fatalf("missing own clone finding: %s", m.Line())
	}
	// doctor --fix runs the check and still deletes no clone folder
	_ = a.DoctorChecks(ctx, true)
	for _, d := range []string{whole.Dir, broken.Dir, cut.Dir} {
		if _, err := os.Stat(d); err != nil {
			t.Fatalf("doctor removed %s: %v", d, err)
		}
	}
	// the note's fix is a real command. It is refused while the clone
	// holds a commit its remote lacks ...
	url := strings.TrimPrefix(n.Fix, "lore store forget ")
	c := &standaloneClones{home: home}
	if _, err := c.ForgetClone(ctx, opsstores.ForgetIn{URL: url}); err == nil || !strings.Contains(err.Error(), "unpushed") {
		t.Fatalf("forget of a clone with unpushed commits: %v", err)
	}
	if _, err := os.Stat(cut.Dir); err != nil {
		t.Fatalf("a refused forget removed %s: %v", cut.Dir, err)
	}
	// ... and, once the remote has it, removes that clone only.
	gitRunT(t, cut.Dir, "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	if _, err := c.ForgetClone(ctx, opsstores.ForgetIn{URL: url}); err != nil {
		t.Fatalf("forget %s: %v", url, err)
	}
	if _, err := os.Stat(cut.Dir); !os.IsNotExist(err) {
		t.Fatalf("forget kept %s: %v", cut.Dir, err)
	}
	if _, err := os.Stat(whole.Dir); err != nil {
		t.Fatalf("forget touched another clone: %v", err)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"/a b/c": "'/a b/c'", "it's": `'it'\''s'`} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDoctorStoreCloneMidMerge (B2-26): a whole store clone left in the
// middle of the person's git merge is named with the commands that finish
// or abort it; a whole clone with nothing in progress is not reported.
func TestDoctorStoreCloneMidMerge(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	p := makeStoreClone(t, home, "owner/notes", "https://host.example/owner/notes")
	a, err := New(Options{Home: home, RepoDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if got, err := a.DoctorStoreClones(ctx); err != nil || len(got) != 0 {
		t.Fatalf("an idle whole clone: %+v, %v", got, err)
	}
	put := func(body string) {
		if err := os.WriteFile(filepath.Join(p.Dir, "placeholder.txt"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitRunT(t, p.Dir, "checkout", "-q", "-b", "side")
	put("side\n")
	gitRunT(t, p.Dir, "commit", "-q", "-am", "side")
	gitRunT(t, p.Dir, "checkout", "-q", "main")
	put("main\n")
	gitRunT(t, p.Dir, "commit", "-q", "-am", "main")
	m := exec.Command("git", "merge", "side")
	m.Dir = p.Dir
	m.Env = append(os.Environ(), "GIT_AUTHOR_NAME=placeholder", "GIT_AUTHOR_EMAIL=placeholder@example.invalid", "GIT_COMMITTER_NAME=placeholder", "GIT_COMMITTER_EMAIL=placeholder@example.invalid")
	if err := m.Run(); err == nil {
		t.Fatal("the merge was meant to conflict")
	}
	got, err := a.DoctorStoreClones(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Message, "in the middle of a git merge") || !strings.Contains(got[0].Fix, "git merge --abort") {
		t.Fatalf("mid-merge clone findings: %+v", got)
	}
}
