package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/store"
)

// testRepoKey is a made-up repo key (12 hex digits), built at run time.
var testRepoKey = strings.Repeat("0a", 6)

// pinnedLayer is a layer wrapper as newLayerBackend pins it, without a
// store behind it: the mode and cache tests need only its decision.
func pinnedLayer(branch, def string, s branchlayer.Settings, settingsErr error) *layerBackend {
	code := codeRepo{Branch: branch, Default: def, Detached: branch == ""}
	l := &layerBackend{code: code, key: branchlayer.Key{RepoKey: testRepoKey, Branch: branch}, settings: s, decideErr: settingsErr}
	if settingsErr == nil {
		l.decision = branchMode(branch, def, s)
	}
	return l
}

// TestLayerCacheFollowsMode (devco:D-411, devco:V-17): the cache path of a
// command follows the same pinned decision its reads follow, on every
// path of the rule: a shared branch (default branch, main-like pattern,
// per-branch share, default_mode share, layers off) uses main's cache
// (LayerCacheDir ""), an isolated one (default rule, isolate pattern,
// per-branch isolate, a stacked branch) its own. Before the fix the cache
// path went by the branch name alone, so dev, staging and a branch marked
// share each kept a layer cache.
func TestLayerCacheFollowsMode(t *testing.T) {
	def := branchlayer.DefaultSettings()
	with := func(edit func(*branchlayer.Settings)) branchlayer.Settings {
		s := branchlayer.DefaultSettings()
		edit(&s)
		return s
	}
	for _, tc := range []struct {
		name, branch string
		s            branchlayer.Settings
		isolated     bool
	}{
		{"default branch", "main", def, false},
		{"default rule isolates", "feat/x", def, true},
		{"stacked branch", "feat/x-part2", def, true},
		{"main-like dev", "dev", def, false},
		{"main-like staging", "staging", def, false},
		{"main-like release pattern", "release/1.2", def, false},
		{"isolate pattern beats main-like", "dev", with(func(s *branchlayer.Settings) { s.Isolate = []string{"dev"} }), true},
		{"per-branch share", "feat/y", with(func(s *branchlayer.Settings) { s.SharedSet = []string{"feat/y"} }), false},
		{"per-branch isolate beats main-like", "staging", with(func(s *branchlayer.Settings) { s.IsolatedSet = []string{"staging"} }), true},
		{"per-branch share beats isolate pattern", "spike/a", with(func(s *branchlayer.Settings) {
			s.Isolate = []string{"spike/*"}
			s.SharedSet = []string{"spike/a"}
		}), false},
		{"default_mode share", "feat/z", with(func(s *branchlayer.Settings) { s.DefaultMode = branchlayer.Shared }), false},
		{"layers off", "feat/x", with(func(s *branchlayer.Settings) { s.Layers = false }), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := pinnedLayer(tc.branch, "main", tc.s, nil)
			iso, err := l.isolated(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if iso != tc.isolated {
				t.Fatalf("%s: isolated %v, want %v", tc.branch, iso, tc.isolated)
			}
			dir := l.LayerCacheDir()
			if (dir != "") != tc.isolated {
				t.Fatalf("%s: cache sub %q while isolated is %v: a shared branch must read main's cache, an isolated one its own", tc.branch, dir, tc.isolated)
			}
			if tc.isolated && !strings.Contains(dir, branchlayer.BranchKey(tc.branch)) {
				t.Fatalf("cache sub %q does not name %s", dir, tc.branch)
			}
		})
	}
}

// TestIsolatedRowsNeverReachMainCache (devco:D-411): the leak guard. For
// every pinned decision, a command whose reads return layer rows (isolated)
// never uses main's cache file (LayerCacheDir ""), and a command on main's
// cache never reads layer rows, whatever LORE_LAYER says and also when the
// policy does not read. It fails if the cache path is chosen by any other
// rule than the reads (by the branch name, or by the default policy).
func TestIsolatedRowsNeverReachMainCache(t *testing.T) {
	policyIsolatedDev := branchlayer.DefaultSettings()
	policyIsolatedDev.IsolatedSet = []string{"dev"}
	sharedFeat := branchlayer.DefaultSettings()
	sharedFeat.SharedSet = []string{"feat/x"}
	bad := errs.New(errs.ConfigInvalid, "policy row pol_x does not read")
	for _, env := range []string{"", "off", "on"} {
		for _, l := range []*layerBackend{
			pinnedLayer("dev", "main", policyIsolatedDev, nil),
			pinnedLayer("dev", "main", branchlayer.DefaultSettings(), nil),
			pinnedLayer("feat/x", "main", sharedFeat, nil),
			pinnedLayer("feat/x", "main", branchlayer.DefaultSettings(), nil),
			pinnedLayer("main", "main", branchlayer.DefaultSettings(), nil),
			pinnedLayer("", "main", branchlayer.DefaultSettings(), nil),
			pinnedLayer("feat/x", "main", branchlayer.DefaultSettings(), bad),
		} {
			t.Setenv(EnvLayer, env)
			iso, err := l.isolated(context.Background())
			if err != nil && l.decideErr == nil {
				t.Fatal(err)
			}
			if iso && l.LayerCacheDir() == "" {
				t.Fatalf("LORE_LAYER=%q branch %q: reads return layer rows into main's cache", env, l.code.Branch)
			}
			if !iso && err == nil && l.LayerCacheDir() != "" {
				t.Fatalf("LORE_LAYER=%q branch %q: a shared read fills a layer cache %q", env, l.code.Branch, l.LayerCacheDir())
			}
			if err != nil && l.LayerCacheDir() == "" {
				t.Fatalf("a policy that does not read put branch %q on main's cache", l.code.Branch)
			}
		}
	}
	// a policy-isolated dev must not use main's cache (the leak a decision
	// from the default policy alone would have made)
	t.Setenv(EnvLayer, "")
	if pinnedLayer("dev", "main", policyIsolatedDev, nil).LayerCacheDir() == "" {
		t.Fatal("policy-isolated dev reads through main's cache")
	}
}

// TestCombinedCacheSplitsIsolatedBranch (devco:D-411): past the per-store
// cache limit every store shares one combined file per checkout; an
// isolated branch gets a file of its own, so its layer rows never land in
// the file the shared branches of the checkout read.
func TestCombinedCacheSplitsIsolatedBranch(t *testing.T) {
	home := t.TempDir()
	shared, err := combinedCachePath(home, "/work/app", "")
	if err != nil {
		t.Fatal(err)
	}
	iso, err := combinedCachePath(home, "/work/app", "_layers/"+testRepoKey+"/feat%2Fx")
	if err != nil {
		t.Fatal(err)
	}
	other, err := combinedCachePath(home, "/work/app", "_layers/"+testRepoKey+"/feat%2Fy")
	if err != nil {
		t.Fatal(err)
	}
	if shared == iso || iso == other || filepath.Dir(shared) != filepath.Dir(iso) {
		t.Fatalf("combined files: shared %s, feat/x %s, feat/y %s", shared, iso, other)
	}
	isoLayer := pinnedLayer("feat/x", "main", branchlayer.DefaultSettings(), nil)
	sharedLayer := pinnedLayer("dev", "main", branchlayer.DefaultSettings(), nil)
	if layerSubOf([]store.Backend{sharedLayer}) != "" || layerSubOf([]store.Backend{sharedLayer, isoLayer}) != isoLayer.LayerCacheDir() {
		t.Fatal("layerSubOf does not follow the pinned decision")
	}
}

// TestBranchPolicyFromFilesMatchesEngine (devco:D-411): the policy read
// from the row files before the caches open (code-repo mode and the store
// layers) gives the settings the engine's own policy read gives, for
// per-branch choices, patterns and the default mode, so the cache path,
// code-repo mode and `lore branch mode` decide by one rule.
func TestBranchPolicyFromFilesMatchesEngine(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	for _, in := range []string{
		`{"key":"branches.isolated","value":"dev"}`,
		`{"key":"branches.shared","value":"feat/shared"}`,
		`{"key":"branches.isolate","value":"spike/*"}`,
		`{"key":"branches.main_like","value":"dev,qa/*"}`,
		`{"key":"branches.default_mode","value":"share"}`,
	} {
		if _, err := run(t, a, settings.OpPolicySet, in, true); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
	}
	st, err := a.state(background())
	if err != nil {
		t.Fatal(err)
	}
	set, err := st.eng.Policy(background(), store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	want := branchSettingsOf(set)
	got, err := repoBranchSettings(a.opts.Home, st.repoTop)
	if err != nil {
		t.Fatal(err)
	}
	fromBackend, err := readBranchPolicy(background(), a.opts.Home, []policySource{{Name: store.SourceRepo, Level: "repo", Backend: st.byName[store.SourceRepo]}})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []branchlayer.Settings{got, branchSettingsOf(fromBackend)} {
		for _, b := range []string{"dev", "feat/shared", "spike/a", "qa/1", "feat/other", "main"} {
			if g, w := branchMode(b, "main", s), branchMode(b, "main", want); g != w {
				t.Fatalf("%s: from the files %+v, from the engine %+v", b, g, w)
			}
		}
	}
	if branchMode("dev", "main", got).Mode != branchlayer.Isolated || branchMode("feat/other", "main", got).Mode != branchlayer.Shared {
		t.Fatalf("settings from the files: %+v", got)
	}
}

// TestBrokenPolicyRowIsNoGuess (devco:D-411): a policy row that does not
// read is an error from the file read, never the default settings: the
// layer wrapper keeps the error (its reads fail, its cache is not main's).
func TestBrokenPolicyRowIsNoGuess(t *testing.T) {
	top := t.TempDir()
	dir := filepath.Join(top, loreFolderName, "data", "policies")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pol_0193f2a7c4b77e1d8a3f5b6c7d8e9f00.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := repoBranchSettings(t.TempDir(), top); err == nil {
		t.Fatal("a policy row that does not read gave settings")
	}
}
