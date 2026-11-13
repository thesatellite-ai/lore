package wire

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/gitenv"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
)

// TestFixChildEnvKeepsRepoRemote (owner items 21, 24): a child of lore
// fix --all after the store clones were fetched skips only store clone
// fetches: its environment blocks no fetch of the repo's own remote (the
// migrate team lock fetches refs/lore/signals there), and --yes given to
// --all reaches the child's arguments. A dry child is offline.
func TestFixChildEnvKeepsRepoRemote(t *testing.T) {
	parent := []string{"PATH=/bin", "HOME=/h"}
	c := fixChild{noStoreFetch: true, yes: true}
	env := c.env(parent)
	if gitenv.NoFetch(env) || gitenv.Offline(env) {
		t.Fatalf("child env blocks the repo's own fetch: %v", env)
	}
	if why, refused := gitenv.NetRefused(env, []string{"fetch", "origin", "refs/lore/signals"}); refused {
		t.Fatalf("the repo's signals fetch is refused in the child: %s", why)
	}
	if !gitenv.NoStoreFetch(env) {
		t.Fatalf("child env lets store clones fetch again: %v", env)
	}
	if args := c.args(); !slices.Contains(args, "--yes") || slices.Contains(args, "--dry-run") {
		t.Fatalf("child args %v", args)
	}
	if args := (fixChild{noStoreFetch: true}).args(); slices.Contains(args, "--yes") {
		t.Fatalf("--yes passed without the caller's confirmation: %v", args)
	}
	dry := fixChild{dry: true, noStoreFetch: true}
	if denv := dry.env(parent); !gitenv.Offline(denv) || !slices.Contains(dry.args(), "--dry-run") {
		t.Fatalf("dry child: env %v args %v", denv, dry.args())
	}
	if fenv := (fixChild{}).env(parent); gitenv.NoStoreFetch(fenv) {
		t.Fatalf("a child whose stores were not all fetched skips store fetches: %v", fenv)
	}
}

// TestFixRepoWalk (owner item 26): --all walks every setup folder for git
// repos holding .lore/, at any depth up to the bound; it does not enter a
// found repo, a hidden folder or a dependency tree; a nested repo comes
// from the known repos; a folder with .lore/ that is no git repo (a setup
// folder root, the home holding ~/.lore) is noted as skipped, never a
// repo to fix; a known repo that is gone is dropped. FP-7: a repo a store
// link names (by remote url) that holds no .lore/ is noted as having no
// lore data, never a repo to fix (a run in it created .lore/).
func TestFixRepoWalk(t *testing.T) {
	base := t.TempDir()
	mk := func(rel string, parts ...string) string {
		t.Helper()
		p := filepath.Join(base, filepath.FromSlash(rel))
		for _, sub := range append([]string{""}, parts...) {
			if err := os.MkdirAll(filepath.Join(p, sub), 0o750); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	repo := func(rel string, lore bool) string {
		if lore {
			return mk(rel, ".git", ".lore")
		}
		return mk(rel, ".git")
	}
	mk("", ".lore") // the setup folder root: not a repo, has .lore (a home)
	a := repo("a", true)
	nested := repo("a/nested", true)
	deep := repo("org/team/client/svc", true)
	linked := repo("org/linked", false)
	repo("org/plain", false)            // no .lore, not linked
	repo("web/node_modules/dep", true)  // a dependency tree
	repo(".cache/hidden", true)         // a hidden folder
	notRepo := mk("org/notes", ".lore") // .lore, no .git
	worktree := mk("wt", ".lore")       // a worktree: .git is a file
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	remotes := map[string][]string{linked: {"github.com/acme/linked"}, filepath.Join(base, "org/plain"): {"github.com/acme/plain"}}
	w := fixRepoWalk{
		known:  []string{nested, filepath.Join(base, "gone")},
		bases:  []string{base, filepath.Join(base, "missing")},
		linked: map[string]bool{"github.com/acme/linked": true},
		remotes: func(_ context.Context, dir string) []string {
			return remotes[resolvedPath(dir)]
		},
	}
	got, err := w.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{a, nested, deep, worktree}
	for i := range want {
		want[i] = resolvedPath(want[i])
	}
	slices.Sort(want)
	if !slices.Equal(got.Roots, want) {
		t.Fatalf("roots:\n%s\nwant:\n%s", strings.Join(got.Roots, "\n"), strings.Join(want, "\n"))
	}
	var skipped []string
	for _, s := range got.Skipped {
		wantWhy := skipNotRepo
		if s.Path == resolvedPath(linked) {
			wantWhy = skipNoLoreData
		}
		if s.Why != wantWhy {
			t.Fatalf("skip %s: why %q", s.Path, s.Why)
		}
		skipped = append(skipped, s.Path)
	}
	if wantSkip := []string{resolvedPath(base), resolvedPath(linked), resolvedPath(notRepo)}; !slices.Equal(skipped, wantSkip) {
		t.Fatalf("skipped %v, want %v", skipped, wantSkip)
	}
	// the depth bound: one level below the base finds only "a" and the
	// worktree (and the known nested repo)
	w.depth = 1
	if got, _ = w.run(context.Background()); len(got.Roots) != 3 {
		t.Fatalf("depth 1 roots %v", got.Roots)
	}
	if s := (opsfix.Skip{Path: "/p", Why: "w"}).Line(); s != "skipped /p: w" {
		t.Fatalf("skip line %q", s)
	}
}

// TestNoProjectNamesUnmigratedRows (owner item 22): in a repo whose rows
// lore 0.1.17 wrote and that was never migrated, finding no project says
// so and points at lore fix, instead of "this repository has no lore
// project"; lore fix's render step after a failed migrate step is
// skipped with that cause, not run into the same error.
func TestNoProjectNamesUnmigratedRows(t *testing.T) {
	repo := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".lore", "data"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".lore", "data", "_meta.json"), []byte(`{"_v": 1, "project_id": "prj_01a1a59b891c70b789144cd3e711fc09"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	v1Row(t, repo, "memories", "mem_01a1a59b891c70b789144cd3e711fc01", `"body":"x","title":"t"`)
	a := newTestApp(t, repo)
	_, err := a.Project(context.Background())
	if err == nil || !strings.Contains(err.Error(), "lore 0.1.17 wrote, which are not migrated yet") {
		t.Fatalf("Project in an unmigrated repo: %v", err)
	}
	st := renderAfterFailedMigrate(repo)
	if st.Failed || st.Changed || !strings.Contains(st.Result, "skipped: the migrate step failed") {
		t.Fatalf("render after a failed migrate: %+v", st)
	}
}

// noProjectRender is a render host that answers as a repository with no
// project does (wire's Project error).
type noProjectRender struct{ a *App }

func (r noProjectRender) Render(ctx context.Context, _ opsrender.RunIn) (opsrender.Answer, error) {
	_, err := r.a.Project(ctx)
	return opsrender.Answer{}, err
}

// TestFixRenderSkipsWithoutProject (owner item 36): a repo with .lore/ but
// no project at all has nothing to render; lore fix's render step says so
// and skips, never failing the repo.
func TestFixRenderSkipsWithoutProject(t *testing.T) {
	repo := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".lore", "data"), 0o750); err != nil {
		t.Fatal(err)
	}
	// a rendered copy from before, so the step renders after a migrate
	if err := os.WriteFile(filepath.Join(repo, ".lore", "LORE.md"), []byte("# old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newTestAppWith(t, repo, func(*Options) {})
	a.opts.Context.Render = noProjectRender{a: a}
	st := a.fixRender(context.Background(), repo, false, true)
	if st.Failed || !strings.HasPrefix(st.Result, renderSkippedNoProject) || !strings.Contains(st.Result, noLoreProject) {
		t.Fatalf("render step in a repo with no project: %+v; want skipped, naming why", st)
	}
}

// staleRender is a render host whose LORE.md is stale until it renders.
type staleRender struct{ renders int }

func (r *staleRender) Render(context.Context, opsrender.RunIn) (opsrender.Answer, error) {
	r.renders++
	return opsrender.Answer{Text: "✓ .lore/LORE.md (10 bytes)\n"}, nil
}

func (r *staleRender) RenderedCopies(context.Context, string) ([]opsrender.CopyFinding, error) {
	if r.renders > 0 {
		return nil, nil
	}
	return []opsrender.CopyFinding{{Kind: opsrender.CopyStale, File: ".lore/LORE.md", Message: ".lore/LORE.md is stale", Fix: "lore render"}}, nil
}

// TestFixRenderAgain (owner item 43): after a doctor repair changed rows,
// FixRenderAgain renders a stale LORE.md once more and its step says it
// ran after the doctor fixes; with nothing stale it renders nothing.
func TestFixRenderAgain(t *testing.T) {
	repo := gitRepo(t)
	a := newTestAppWith(t, repo, func(*Options) {})
	h := &staleRender{}
	a.opts.Context.Render = h
	st := a.FixRenderAgain(context.Background())
	if h.renders != 1 || !st.Changed || st.Failed || st.Step != opsdoctor.StepRender || !strings.HasPrefix(st.Result, renderAgainPrefix+"rendered") {
		t.Fatalf("render again: %+v after %d render(s)", st, h.renders)
	}
	st = a.FixRenderAgain(context.Background())
	if h.renders != 1 || st.Changed || st.Result != opsdoctor.ResultCurrent {
		t.Fatalf("nothing stale: %+v after %d render(s)", st, h.renders)
	}
}
