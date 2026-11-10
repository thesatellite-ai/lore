package wire

// setupprefillcache.go — the App's per-process memory of setup.plan's
// detections (ONB 6.1 "the page and the CLI do not probe twice in a row").
//
//	setup.plan ─▶ prefills ─▶ setupProber.Prefill ─▶ prefillCache.get (hit: no probe)
//	                                              └▶ probe (ssh -T per alias, gh, key scans) ─▶ put
//
// The setup page calls setup.plan when a setup opens, again after the
// kind step is saved and again on the summary; each run probes every
// GitHub alias over ssh (about a second per alias). The same detections
// are reused for opsdoctor.CacheTTL so those repeats answer at once.
// Failed detections are never kept (the next call tries again). Memory
// only, never written anywhere, gone with the process.

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// prefillCache keeps Prefill answers by detection and setup entry.
type prefillCache struct {
	ttl time.Duration
	now func() time.Time

	mu sync.Mutex
	m  map[string]prefillHit
}

// prefillHit is one kept answer and when it was probed.
type prefillHit struct {
	at  time.Time
	out opssetup.PrefillOut
}

// newPrefillCache keeps answers for opsdoctor.CacheTTL on clock now.
func newPrefillCache(now func() time.Time) *prefillCache {
	if now == nil {
		now = time.Now
	}
	return &prefillCache{ttl: opsdoctor.CacheTTL, now: now, m: map[string]prefillHit{}}
}

// prefillCacheKey is id plus the whole setup entry: a detection reads the
// entry's alias, user and key, so an edited entry never reuses an old
// answer. ok=false (the entry does not encode): do not cache.
func prefillCacheKey(id onboard.PrefillID, setup onboard.SetupEntry) (string, bool) {
	b, err := json.Marshal(setup)
	if err != nil {
		return "", false
	}
	return string(id) + "\x00" + string(b), true
}

// get is the kept answer for key when it is younger than the TTL, and
// when it was probed (for the trace's "cached, from <time>" call).
func (c *prefillCache) get(key string) (opssetup.PrefillOut, time.Time, bool) {
	if c == nil {
		return opssetup.PrefillOut{}, time.Time{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.m[key]
	if !ok || c.now().Sub(h.at) >= c.ttl {
		return opssetup.PrefillOut{}, time.Time{}, false
	}
	return h.out, h.at, true
}

// put keeps out under key.
func (c *prefillCache) put(key string, out opssetup.PrefillOut) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = prefillHit{at: c.now(), out: out}
}
