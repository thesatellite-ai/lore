package wire_test

// ONBOARDING_DESIGN 6.1 / Q11: known_repos is touched at most once an hour
// per root, lives in the machine store, and doctor --all falls back to a
// folder scan only while the table is empty.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/wire"
)

// underBase is a stand-in FolderMatch for `<base>/**` globs (the real one
// is logic/onboard's).
func underBase(glob, root string) bool {
	base := strings.TrimSuffix(glob, "/**")
	return root == base || strings.HasPrefix(root, base+string(filepath.Separator))
}

func TestTouchKnownRepoThrottles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t0 := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	h := opstest.New(t, opstest.Fixed(t0))

	// Not initialised (no repo id): nothing recorded.
	if err := h.App.TouchKnownRepo(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := h.App.KnownRepos(ctx); err != nil || len(got) != 0 {
		t.Fatalf("before a repo id: %v, %v", got, err)
	}

	repoID := initRepo(t, h)
	if err := h.App.TouchKnownRepo(ctx); err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.EvalSymlinks(h.Repo)
	want := onboard.KnownRepo{RepoID: repoID, Root: root, LastSeen: t0}
	check := func(app *wire.App, w onboard.KnownRepo) {
		t.Helper()
		got, kerr := app.KnownRepos(ctx)
		if kerr != nil || len(got) != 1 || got[0].RepoID != w.RepoID || got[0].Root != w.Root || !got[0].LastSeen.Equal(w.LastSeen) {
			t.Fatalf("known repos = %+v, %v; want [%+v]", got, kerr, w)
		}
	}
	check(h.App, want)
	if m, _ := filepath.Glob(filepath.Join(h.Repo, ".lore", "data", "known_repos", "*")); len(m) != 0 {
		t.Fatalf("known_repos reached the repo store: %v", m)
	}

	// A process that never opened the app (lore version) touches nothing,
	// even an hour later.
	cold := h.Reopen(opstest.Fixed(t0.Add(2 * wire.KnownRepoTouchEvery)))
	if err := cold.App.TouchKnownRepo(ctx); err != nil {
		t.Fatal(err)
	}
	check(cold.App, want)

	// 59 minutes later: throttled, last_seen unchanged.
	soon := cold.Reopen(opstest.Fixed(t0.Add(KnownRepoTouchEveryMinus())))
	openApp(t, soon.App)
	if err := soon.App.TouchKnownRepo(ctx); err != nil {
		t.Fatal(err)
	}
	check(soon.App, want)

	// An hour later: refreshed in place, still one row.
	later := t0.Add(wire.KnownRepoTouchEvery)
	h2 := soon.Reopen(opstest.Fixed(later))
	openApp(t, h2.App)
	if err := h2.App.TouchKnownRepo(ctx); err != nil {
		t.Fatal(err)
	}
	want.LastSeen = later
	check(h2.App, want)
}

// initRepo gives h's checkout a project, _meta.json and a repo id, as
// lore init does (opstest.New already holds the project row), and
// returns the id.
func initRepo(t *testing.T, h *opstest.H) string {
	t.Helper()
	ctx := context.Background()
	pid, err := h.App.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.App.SeedRepoMeta(ctx, pid); err != nil {
		t.Fatal(err)
	}
	id, _, err := h.App.EnsureRepoIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// openApp opens app's stores, as any command that reads or writes does.
func openApp(t *testing.T, app *wire.App) {
	t.Helper()
	if _, err := app.Engine(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// KnownRepoTouchEveryMinus is just inside the throttle window.
func KnownRepoTouchEveryMinus() time.Duration { return wire.KnownRepoTouchEvery - time.Minute }

// mkProject makes dir/.lore (and the folders above it).
func mkProject(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	r, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestKnownReposUnderFallsBackToScan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agency := filepath.Join(work, "agency")
	acme := mkProject(t, filepath.Join(agency, "acme-hub"))
	deep := mkProject(t, filepath.Join(agency, "clients", "mira", "site")) // depth 3: found
	mkProject(t, filepath.Join(agency, "a", "b", "c", "too-deep"))         // depth 4: not
	mkProject(t, filepath.Join(agency, ".cache", "hidden"))                // hidden: not
	mkProject(t, filepath.Join(acme, "web"))                               // inside a project: not
	mkProject(t, filepath.Join(work, "tomas", "notes"))                    // outside the glob
	globs := []string{agency + "/**", filepath.Join(work, "missing") + "/**"}

	h := opstest.New(t)
	got, scanned, err := h.App.KnownReposUnder(ctx, globs, underBase, 3)
	if err != nil || !scanned {
		t.Fatalf("scanned %v err %v", scanned, err)
	}
	var roots []string
	for _, r := range got {
		roots = append(roots, r.Root)
	}
	if strings.Join(roots, ",") != acme+","+deep {
		t.Fatalf("scan found %v; want [%s %s]", roots, acme, deep)
	}

	// Once known_repos has a row, the table answers and nothing is scanned.
	initRepo(t, h)
	if err = h.App.TouchKnownRepo(ctx); err != nil {
		t.Fatal(err)
	}
	repoRoot, _ := filepath.EvalSymlinks(h.Repo)
	got, scanned, err = h.App.KnownReposUnder(ctx, globs, underBase, 3)
	if err != nil || scanned || len(got) != 0 {
		t.Fatalf("with a known repo outside the globs: %+v scanned %v err %v", got, scanned, err)
	}
	got, scanned, err = h.App.KnownReposUnder(ctx, []string{filepath.Dir(repoRoot) + "/**"}, underBase, 3)
	if err != nil || scanned || len(got) != 1 || got[0].Root != repoRoot {
		t.Fatalf("known repo under its glob: %+v scanned %v err %v", got, scanned, err)
	}
}

func TestGlobBase(t *testing.T) {
	t.Parallel()
	for glob, want := range map[string]string{
		"/w/agency/**":           "/w/agency",
		"/w/agency/*/site":       "/w/agency",
		"/w/agency":              "/w/agency",
		"/w/clients/acme-[ab]/x": "/w/clients",
		"/**":                    "/",
	} {
		if got := wire.GlobBase(glob); got != filepath.FromSlash(want) {
			t.Errorf("GlobBase(%q) = %q, want %q", glob, got, want)
		}
	}
}
