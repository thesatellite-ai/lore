package renderhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// TestCommitRenderStagesNewFullFile is devco:D-759: a project made by an
// older lore commits .lore/LORE.md but never had LORE.full.md. The commit
// hook's refresh writes the full file, and it must go into the same commit
// as LORE.md; leaving it out left `?? .lore/LORE.full.md` in git status
// right after the commit `lore migrate run` prints. A full file git
// ignores, or one lore did not write, stays out of the commit.
//
// devco:D-781: only a full file the refresh created is new. One that was
// on disk before the commit and that git does not track (the person ran
// `lore render` and kept it out, or `git rm --cached` it) is never staged:
// staging it committed internal notes against the person's choice.
func TestCommitRenderStagesNewFullFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=T", "-c", "user.email=t@example.test"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write(LoreMDRel, "rules\n")
	git("add", LoreMDRel)
	git("commit", "-q", "-m", "render from an older lore")

	created := map[string]bool{LoreMDRel: true} // on disk before the refresh: LORE.md only
	write(LoreFullRel, buildFullFile(nil, nil)) // what the refresh just wrote
	if got, fresh := commitRenderStage(ctx, root, created); !slices.Equal(got, []string{LoreMDRel, LoreFullRel}) || !slices.Equal(fresh, []string{LoreFullRel}) {
		t.Fatalf("new lore-written full file beside a tracked LORE.md: stage %v (new %v), want both render files, the full one new", got, fresh)
	}

	kept := map[string]bool{LoreMDRel: true, LoreFullRel: true} // the person's untracked full file was already there
	if got, _ := commitRenderStage(ctx, root, kept); !slices.Equal(got, []string{LoreMDRel}) {
		t.Fatalf("an untracked full file that was on disk before the refresh: stage %v, want only LORE.md (D-781)", got)
	}

	write(LoreFullRel, "my own notes\n")
	if got, _ := commitRenderStage(ctx, root, created); !slices.Equal(got, []string{LoreMDRel}) {
		t.Fatalf("a LORE.full.md lore did not write: stage %v, want only LORE.md", got)
	}

	write(LoreFullRel, buildFullFile(nil, nil))
	write(".git/info/exclude", LoreFullRel+"\n") // a teammate's clone keeps it local
	if got, _ := commitRenderStage(ctx, root, created); !slices.Equal(got, []string{LoreMDRel}) {
		t.Fatalf("an ignored full file: stage %v, want only LORE.md", got)
	}
}

// TestCommitRenderClientRepoAddsNoFile is devco:D-781's client side: the
// commit hook adds a new LORE.full.md beside a tracked LORE.md in an own
// repo (D-759), but a client repo gets no file from lore it does not
// already track (D-754). There the new full file leaves the stage and goes
// to the clone's .git/info/exclude, so git status stays clean; the tracked
// LORE.md is still staged. A repo whose class lore cannot tell is treated
// as a client repo (fail closed).
func TestCommitRenderClientRepoAddsNoFile(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		client   func(context.Context) (bool, error)
		wantFull bool
	}{
		{"own repo", func(context.Context) (bool, error) { return false, nil }, true},
		{"client repo", func(context.Context) (bool, error) { return true, nil }, false},
		{"class unknown", func(context.Context) (bool, error) { return false, os.ErrPermission }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			git := func(args ...string) string {
				t.Helper()
				out, err := exec.Command("git", append([]string{"-C", root, "-c", "user.name=T", "-c", "user.email=t@example.test"}, args...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
				return string(out)
			}
			git("init", "-q")
			if err := os.MkdirAll(filepath.Join(root, ".lore"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(LoreMDRel)), []byte("rules\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			git("add", LoreMDRel)
			git("commit", "-q", "-m", "render from an older lore")
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(LoreFullRel)), []byte(buildFullFile(nil, nil)), 0o600); err != nil {
				t.Fatal(err)
			}
			h := New(Options{RepoIsClient: tc.client})
			got := h.keepNewRenderLocalInClient(ctx, root, []string{LoreMDRel, LoreFullRel}, []string{LoreFullRel})
			want := []string{LoreMDRel}
			if tc.wantFull {
				want = append(want, LoreFullRel)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("stage %v, want %v", got, want)
			}
			if st := git("status", "--porcelain"); !tc.wantFull && st != "" {
				t.Fatalf("the new full file kept out of a client commit is still in git status:\n%s", st)
			}
		})
	}
}
