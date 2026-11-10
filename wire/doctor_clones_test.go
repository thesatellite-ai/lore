package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

// makeStoreClone lays out a whole clone at <home>/clones/<norm> the way the
// git backend does: the bare clone <stem>.git and its worktree <stem>/work.
func makeStoreClone(t *testing.T, home, norm string) gitbackend.ClonePaths {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	seed := filepath.Join(t.TempDir(), "seed")
	gitRunT(t, t.TempDir(), "init", "-q", "--initial-branch=main", seed)
	if err := os.WriteFile(filepath.Join(seed, "placeholder.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRunT(t, seed, "add", "placeholder.txt")
	gitRunT(t, seed, "commit", "-q", "-m", "init")
	p := gitbackend.PathsOf(filepath.Join(home, gitbackend.ClonesDir, filepath.FromSlash(norm)))
	if err := os.MkdirAll(filepath.Dir(p.Bare), 0o700); err != nil {
		t.Fatal(err)
	}
	gitRunT(t, seed, "clone", "-q", "--bare", seed, p.Bare)
	gitRunT(t, p.Bare, "worktree", "add", "-q", p.Work, "main")
	return p
}

// TestDoctorStoreClones: a whole clone (its <repo>/ and <repo>.git/ side by
// side) is never reported; a clone that lost a half is named with the
// command that removes it, and the store forget the fix names removes
// exactly that clone. Doctor itself, --fix included, deletes nothing.
func TestDoctorStoreClones(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	whole := makeStoreClone(t, home, "host.example/owner/notes")
	noBare := makeStoreClone(t, home, "host.example/owner/nobare")
	noWork := makeStoreClone(t, home, "host.example/owner/nowork@feature")
	orphan := makeStoreClone(t, home, "host.example/owner/orphan")
	if err := os.RemoveAll(filepath.Join(orphan.Bare, "worktrees")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(noBare.Bare); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(noWork.Base); err != nil {
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
	byBase := map[string]DoctorFinding{}
	for _, f := range got {
		if f.Check != DoctorCheckStoreClone {
			t.Fatalf("check %q", f.Check)
		}
		if strings.Contains(f.Line(), whole.Base) {
			t.Fatalf("a whole clone was reported: %s", f.Line())
		}
		for _, p := range []gitbackend.ClonePaths{noBare, noWork, orphan} {
			if strings.Contains(f.Message, p.Base) {
				byBase[p.Base] = f
			}
		}
	}
	o := byBase[orphan.Base]
	if o.Level != DoctorWarning || !strings.Contains(o.Message, orphan.Base+" is unused: its worktree names git data that is gone") || o.Fix != "rm -rf '"+orphan.Base+"'" {
		t.Fatalf("orphan worktree finding: %s", o.Line())
	}
	w := byBase[noBare.Base]
	if w.Level != DoctorWarning || !strings.Contains(w.Message, noBare.Base+" is unused") || !strings.Contains(w.Message, noBare.Bare+" is missing") || w.Fix != "rm -rf '"+noBare.Base+"'" {
		t.Fatalf("no-git-data finding: %s", w.Line())
	}
	n := byBase[noWork.Base]
	if n.Level != DoctorNote || !strings.Contains(n.Message, noWork.Bare) || n.Fix != "lore store forget https://host.example/owner/nowork" {
		t.Fatalf("no-worktree finding: %s", n.Line())
	}
	// doctor --fix runs the check and still deletes no clone folder
	_ = a.DoctorChecks(ctx, true)
	for _, d := range []string{whole.Base, whole.Bare, noBare.Base, noWork.Bare, orphan.Base, orphan.Bare} {
		if _, err := os.Stat(d); err != nil {
			t.Fatalf("doctor removed %s: %v", d, err)
		}
	}
	// the note's fix is a real command. It is refused while the clone
	// holds commits its remote lacks (makeStoreClone's bare clone has no
	// origin branches yet) ...
	url := strings.TrimPrefix(n.Fix, "lore store forget ")
	c := &standaloneClones{home: home}
	if _, err := c.ForgetClone(ctx, opsstores.ForgetIn{URL: url}); err == nil || !strings.Contains(err.Error(), "unpushed") {
		t.Fatalf("forget of a clone with unpushed commits: %v", err)
	}
	if _, err := os.Stat(noWork.Bare); err != nil {
		t.Fatalf("a refused forget removed %s: %v", noWork.Bare, err)
	}
	// ... and, once the remote has them, removes that clone only.
	gitRunT(t, noWork.Bare, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	gitRunT(t, noWork.Bare, "fetch", "-q", "origin")
	if _, err := c.ForgetClone(ctx, opsstores.ForgetIn{URL: url}); err != nil {
		t.Fatalf("forget %s: %v", url, err)
	}
	if _, err := os.Stat(noWork.Bare); !os.IsNotExist(err) {
		t.Fatalf("forget kept %s: %v", noWork.Bare, err)
	}
	if _, err := os.Stat(whole.Bare); err != nil {
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
