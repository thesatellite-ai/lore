package wire

// doctor_cache.go — the doctor check cache shared by every lore process on
// this machine (ONBOARDING_DESIGN 6.1, #17): `lore doctor`, `lore setup`
// and the page server reuse one another's results for 60 seconds instead
// of each starting cold.
//
//	opsdoctor.CacheEnv.DoctorCache ─▶ machineDoctorCache (one machine.db
//	    handle for the run; closed by ops/doctor when the run ends)
//	    Get ─▶ sqlite.DoctorCache row ─ stamp equal and younger than the TTL
//	         ─▶ the stored CheckResult (trace: "cached, from HH:MM:SS")
//	    Put ─▶ upsert + prune rows past the TTL (one transaction)
//	opsdoctor.StampEnv.DoctorStamp ─▶ what a setup's checks look at:
//	    its entry (entryFingerprint), its ssh key file's size and time, its
//	    org entry and the personal store url, so an edit of any of them is
//	    a miss at once (invalidation without anyone having to clear rows)
//
// The cache is bookkeeping: a machine.db that does not open falls back to
// the App's in-process cache, and a failed read or write is a miss, never
// a failed doctor run.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// machineDoctorCache is opsdoctor.Cache over the doctor_cache table of the
// machine store file. ctx is the opening call's context with its
// cancellation removed (the Cache interface has none, and the App keeps
// this cache across requests, so a finished request must not cancel it).
type machineDoctorCache struct {
	ctx context.Context
	db  *sqlite.DoctorCache
	ttl time.Duration
	now func() time.Time
}

// openMachineDoctorCache opens the shared cache of the lore home home.
func openMachineDoctorCache(ctx context.Context, home string, now func() time.Time) (*machineDoctorCache, error) {
	db, err := sqlite.OpenDoctorCache(ctx, filepath.Join(home, filepath.FromSlash(MachineStoreFile)))
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &machineDoctorCache{ctx: context.WithoutCancel(ctx), db: db, ttl: opsdoctor.CacheTTL, now: now}, nil
}

// dbKey is the table key of k (its stamp is compared, not keyed: one row
// per check and target, replaced when the inputs change).
func dbKey(k opsdoctor.CacheKey) sqlite.DoctorCacheKey {
	return sqlite.DoctorCacheKey{Setup: k.Setup, Check: string(k.Check), Target: k.Target}
}

// Get implements opsdoctor.Cache: a result younger than the TTL made under
// the same stamp, by any lore process on this machine.
func (c *machineDoctorCache) Get(k opsdoctor.CacheKey) (onboard.CheckResult, bool) {
	e, ok, err := c.db.Get(c.ctx, dbKey(k))
	if err != nil || !ok || e.Stamp != k.Stamp || !opsdoctor.Fresh(e.CheckedAt, c.now(), c.ttl) {
		return onboard.CheckResult{}, false
	}
	var r onboard.CheckResult
	if json.Unmarshal(e.Doc, &r) != nil || r.Check != k.Check {
		return onboard.CheckResult{}, false // not a result this cache wrote: re-probe
	}
	return r, true
}

// Put implements opsdoctor.Cache. A failed write only means the next run
// probes again.
func (c *machineDoctorCache) Put(k opsdoctor.CacheKey, r onboard.CheckResult) {
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	_ = c.db.Put(c.ctx, dbKey(k), sqlite.DoctorCacheEntry{Stamp: k.Stamp, CheckedAt: r.CheckedAt, Doc: b}, c.now().Add(-c.ttl))
}

// Close releases the machine.db handle (io.Closer: ops/doctor closes it).
func (c *machineDoctorCache) Close() error { return c.db.Close() }

// DoctorStamp implements opsdoctor.StampEnv: a hash of everything outside
// the entry's own fields that its checks read, plus the entry. Each part
// that cannot be read adds a fixed marker, so "unreadable" is a state of
// its own and a later readable value is a change.
func (a *App) DoctorStamp(_ context.Context, e onboard.SetupEntry) string {
	h := sha256.New()
	part := func(s string) {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	part(entryFingerprint(e, true))
	part(keyFileState(e.GitHub.SSHKey))
	org := ""
	if e.Org != "" {
		if cfg, _, err := orgcfg.LoadConfig(a.opts.Home); err == nil {
			for _, o := range cfg.Orgs {
				if o.Name == e.Org {
					if b, merr := json.Marshal(o); merr == nil {
						org = string(b)
					}
				}
			}
		} else {
			org = stampUnreadable
		}
	}
	part(org)
	part(rosterCloneStamp(a.opts.Home, e.Org)) // identity.roster/period read the roster: a sync that changes it re-probes (B2-24)
	personal := stampUnreadable
	if mc, err := readMachineConfig(a.opts.Home); err == nil {
		personal = mc.personalURL()
	}
	part(personal)
	part(topIdentityStamp(a.opts.Home)) // identity.top_level reads it (doctor_identitysource.go)
	return hex.EncodeToString(h.Sum(nil))
}

// stampUnreadable stands for a stamp input that could not be read.
const stampUnreadable = "\x00unreadable"

// keyFileState is the ssh key file's size and change time ("" when the
// setup records no key): a key regenerated at the same path is a change.
func keyFileState(key string) string {
	if key == "" {
		return ""
	}
	home, _ := os.UserHomeDir() // "" leaves a ~ path unexpanded: then it is just missing
	fi, err := os.Stat(onboard.ExpandHome(key, home))
	if err != nil {
		return stampUnreadable
	}
	return strconv.FormatInt(fi.Size(), 10) + "@" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
}
