package wire

// cache_prune.go — the lore home's cache folder for the Local cache
// operations (`lore cache prune`, P3-MISC-2), the automatic once-a-day
// sweep every command starts after its output, and `lore doctor`'s
// cache_size note (public issue #178; ops/cache/sweep.go holds the rules).

import (
	"context"
	"path/filepath"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	opscache "github.com/thesatellite-ai/lore/ops/cache"
)

// LoreHome is the lore home this App was built with (~/.lore), for the
// Local operations that work on its folders (opscache.Env, `lore cache
// prune`, P3-MISC-2).
func (a *App) LoreHome() string { return a.opts.Home }

// CacheRules are cache.max_age and cache.max_size from config.yaml; an
// unreadable file keeps the defaults (`lore config check` reports it).
func (a *App) CacheRules() opscache.Rules {
	r, _ := a.cacheSettings()
	return r
}

// cacheSettings are the sweep's rules and whether cache.auto_prune is on.
func (a *App) cacheSettings() (opscache.Rules, bool) {
	var cfg *orgconf.Config
	if c, _, err := orgcfg.LoadConfig(a.opts.Home); err == nil {
		cfg = c
	}
	return opscache.Rules{MaxAge: cfg.CacheMaxAge(), MaxSize: cfg.CacheMaxSize()}, cfg.CacheAutoPrune()
}

// CacheAutoPruneSkips reports commands after which the automatic sweep
// never runs: help and completion print only, the merge driver runs inside
// git, and `lore cache …` is the person's own prune.
func CacheAutoPruneSkips(first string) bool {
	switch first {
	case "", "help", "completion", "__complete", "__completeNoDesc", "merge-driver", opscache.Noun:
		return true
	}
	return false
}

// CacheAutoPrune runs the once-a-day cache sweep (ops/cache.AutoPrune):
// silent, lock-safe, capped in time, and it never fails the command. A dry
// run or a home lore cannot write leaves the cache as it is.
func (a *App) CacheAutoPrune(ctx context.Context) {
	home := a.opts.Home
	if home == "" || dryRunProcess() || !homeWritable(home) {
		return
	}
	rules, on := a.cacheSettings()
	if !on {
		return
	}
	_, _ = opscache.AutoPrune(ctx, filepath.Join(home, cacheDir), rules, time.Now()) // best effort: the next pass retries
}

// DoctorCheckCacheSize names the doctor check.
const DoctorCheckCacheSize DoctorCheck = "cache_size"

// DoctorCacheSize notes how big the cache folder is and how much of it is
// stale, with the command that removes the stale part now (a note: lore
// removes it by itself once a day, nothing is broken). A cache with
// nothing stale prints nothing.
func (a *App) DoctorCacheSize(context.Context) ([]DoctorFinding, error) {
	root := filepath.Join(a.opts.Home, cacheDir)
	rules, on := a.cacheSettings()
	rules.Grace = opscache.AutoGrace
	sw, err := opscache.MakeSweep(root, rules, time.Now())
	if err != nil || len(sw.Stale) == 0 {
		return nil, nil // unreadable cache: nothing to say here; empty: all fine
	}
	when := "lore removes it by itself once a day"
	if !on {
		when = "cache.auto_prune is off, so it stays until you prune"
	}
	return []DoctorFinding{{Check: DoctorCheckCacheSize, Level: DoctorNote,
		Message: root + " holds " + opscache.HumanBytes(sw.Bytes) + " in " + strconv.Itoa(sw.Files) + " cache file(s); " +
			opscache.HumanBytes(sw.StaleBytes) + " in " + strconv.Itoa(len(sw.Stale)) + " of them is stale (repo copies gone or moved, unused for longer than cache.max_age " +
			humanDays(rules.MaxAge) + ", or past cache.max_size " + opscache.HumanBytes(rules.MaxSize) + "): " + when,
		Fix: "lore cache prune --yes"}}, nil
}

// humanDays prints d as whole days when it is one (14d), else Go syntax.
func humanDays(d time.Duration) string {
	if d > 0 && d%orgconf.Day == 0 {
		return strconv.FormatInt(int64(d/orgconf.Day), 10) + "d"
	}
	return d.String()
}
