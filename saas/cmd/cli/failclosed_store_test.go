package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/stores"
)

// failingGit runs real git but fails every invocation whose arguments
// include failArg (a git subcommand such as "for-each-ref"), as a broken
// or locked clone would.
type failingGit struct {
	failArg string
}

func (g failingGit) Run(ctx context.Context, inv gitref.Invocation) ([]byte, error) {
	if slices.Contains(inv.Args, g.failArg) {
		return nil, errors.New("injected git failure: " + g.failArg)
	}
	return gitref.Exec{}.Run(ctx, inv)
}

// testStoreURL is a store URL the layout accepts.
const testStoreURL = "https://example.com/agency/notes"

// storeTestManager returns a manager rooted in a temp lore home, the config
// naming testStoreURL, and the clone dir that URL maps to.
func storeTestManager(t *testing.T, git gitref.Git) (*stores.Manager, *lorecfg.Config, string) {
	t.Helper()
	m := &stores.Manager{Layout: stores.Layout{Home: t.TempDir()}, Git: git}
	cfg := &lorecfg.Config{Orgs: []lorecfg.Org{{Name: "northwind", Stores: []lorecfg.Store{{URL: testStoreURL}}}}}
	dir, err := m.Layout.CloneDir(testStoreURL)
	if err != nil {
		t.Fatal(err)
	}
	return m, cfg, dir
}

// initBareClone makes a real bare repository at dir.
func initBareClone(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := testCmd(t, "git", "init", "-q", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v %s", err, out)
	}
}

func hasProblem(v *storeView, prefix string) bool {
	for _, p := range v.Problems {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// TestCollectStoresReportsUnopenableClone: a clone folder that exists but
// cannot be opened is a problem, never "not cloned yet" (it may hold
// unpushed work).
func TestCollectStoresReportsUnopenableClone(t *testing.T) {
	m, cfg, dir := storeTestManager(t, nil)
	// Looks like a bare repo (HEAD, objects, refs) but git cannot open it.
	for _, d := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	views, err := collectStores(t.Context(), cfg, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Cloned || !hasProblem(views[0], "open the clone: ") {
		t.Fatalf("an unopenable clone must be a problem: %+v", views[0])
	}
}

// TestCollectStoresNotClonedIsNotAProblem: the ordinary "never cloned"
// case stays a quiet not-cloned row.
func TestCollectStoresNotClonedIsNotAProblem(t *testing.T) {
	m, cfg, _ := storeTestManager(t, nil)
	views, err := collectStores(t.Context(), cfg, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Cloned || len(views[0].Problems) != 0 {
		t.Fatalf("never cloned: want not cloned and no problems: %+v", views[0])
	}
}

// TestStoreShowAndDoctorReportUnpushedReadFailure: when the clone's refs
// cannot be listed, show and doctor say so instead of reporting "nothing
// unpushed", and doctor reports the branch check that could not run
// instead of "branch does not exist".
func TestStoreShowAndDoctorReportUnpushedReadFailure(t *testing.T) {
	m, cfg, dir := storeTestManager(t, failingGit{failArg: "for-each-ref"})
	initBareClone(t, dir)
	views, err := collectStores(t.Context(), cfg, m)
	if err != nil {
		t.Fatal(err)
	}
	v := views[0]
	if !v.Cloned || !hasProblem(v, unpushedUnknownProblem) {
		t.Fatalf("store show must report the unpushed check it could not run: %+v", v)
	}
	dv := &storeView{URL: v.URL, Branches: v.Branches, Unpushed: []string{}, Problems: []string{}}
	doctorCheckStore(t.Context(), m, dv)
	if !hasProblem(dv, unpushedUnknownProblem) {
		t.Fatalf("doctor must report the unpushed check it could not run: %v", dv.Problems)
	}
	for _, b := range dv.Branches {
		if !hasProblem(dv, "branch "+b+": ") || hasProblem(dv, "branch "+b+" does not exist") {
			t.Fatalf("doctor must report the failed branch check, not a missing branch: %v", dv.Problems)
		}
	}
}
