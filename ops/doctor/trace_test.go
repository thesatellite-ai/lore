package doctor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/logic/onboard"
)

// A cached doctor result still shows on the page's per-check Details: one
// "cache" call labelled with the check id, carrying when it was probed.
func TestAllCacheHitsTraced(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home)}
	env.cache = NewMemCache(0, time.Now)
	ce := cachedEnv{env}
	runAllT(t, ce, RunIn{})
	first := len(env.prober.reqs)
	probed := 0 // the results the second run may take from the cache
	for _, r := range env.prober.reqs {
		if Cacheable(r.Check) {
			probed++
		}
	}
	if probed == 0 {
		t.Fatal("nothing probed")
	}
	ctx, tr := probe.WithTrace(context.Background())
	if _, err := Run(ctx, ce, RunIn{All: true}); err != nil {
		t.Fatal(err)
	}
	for _, r := range env.prober.reqs[first:] {
		if Cacheable(r.Check) {
			t.Fatalf("second run probed %s again", r.Check.ID)
		}
	}
	calls := tr.Calls()
	if len(calls) != probed {
		t.Fatalf("%d cache calls for %d cached checks", len(calls), probed)
	}
	labels := map[string]bool{}
	for _, c := range calls {
		if c.Kind != probe.CallCache || c.CachedAt.IsZero() || !strings.HasPrefix(c.Command, "cached, from ") {
			t.Errorf("call = %+v", c)
		}
		labels[c.Label] = true
	}
	if !labels[string(onboard.CheckGitHubSSHAuth)] {
		t.Errorf("no call labelled %s: %v", onboard.CheckGitHubSSHAuth, labels)
	}
	// --fresh probes: no cache calls.
	ctx, tr = probe.WithTrace(context.Background())
	if _, err := Run(ctx, ce, RunIn{All: true, Fresh: true}); err != nil {
		t.Fatal(err)
	}
	if n := len(tr.Calls()); n != 0 {
		t.Errorf("--fresh recorded %d cache calls", n)
	}
}

// A store that is not on GitHub (e.g. a local folder) gets the catalog's
// FixNotGitHub store.write text naming the store, and a template that
// cannot be filled reads as plain advice: never the raw E_FIX_REFUSED error.
func TestFixRefusedIsPlain(t *testing.T) {
	c, ok := checkFor(onboard.CheckStoreWrite, onboard.KindSolo, onboard.StoreRoleMe)
	if !ok {
		t.Fatal("no store.write check")
	}
	sr := &setupRun{now: time.Now}
	it := item{setup: personal(t.TempDir()), check: c, target: TargetStore(storeMe)}
	f := sr.fix(it, map[string]string{onboard.VarURL: "file:///home/pat/stores/me", onboard.VarFixVariant: string(onboard.FixNotGitHub)})
	if strings.Contains(f.Text, "E_FIX_REFUSED") || strings.Contains(f.Text, "has no value") ||
		!strings.Contains(f.Text, "file:///home/pat/stores/me") || len(f.Command) != 0 || f.Runnable {
		t.Errorf("fix = %+v", f)
	}
	// With the value present the catalog's own text is used.
	f = sr.fix(it, map[string]string{onboard.VarURL: "git@github.com:octo-personal/notes.git", onboard.VarOwner: "octo-personal", onboard.VarRepo: "notes"})
	if !strings.Contains(f.Text, "notes") || strings.Contains(f.Text, "No ready-made fix") {
		t.Errorf("filled fix = %+v", f)
	}
	if got := refusedFixText(onboard.Check{ID: onboard.CheckStoreRead}); !strings.Contains(got, string(onboard.CheckStoreRead)) {
		t.Errorf("no title: %q", got)
	}
}
