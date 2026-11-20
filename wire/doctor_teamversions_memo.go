package wire

// doctor_teamversions_memo.go — one team-versions read per cache file in a
// plain doctor run (D-739). The read parses every row's JSON in the cache
// (the `_w` stamps, adapters/cache/sqlite team_versions.go), and two
// read-only checks asked for it (team_versions and actor_roster): about
// 0.2 s of CPU each on a 2,400-task project. A plain doctor run's checks
// only read, so they share one answer per cache file; a fixing run (whose
// repairs write stamped rows) and every other command read the cache as
// before.

import (
	"context"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
)

// teamVersionsMemo holds one team-versions read per cache file.
type teamVersionsMemo struct {
	mu  sync.Mutex
	got map[*sqlite.Cache]*teamVersionsRead
}

// teamVersionsRead is one cache file's read: once fills tvs and err.
type teamVersionsRead struct {
	once sync.Once
	tvs  []sqlite.TeamVersion
	err  error
}

// teamVersionsMemoKey is the context key of withTeamVersionsMemo.
type teamVersionsMemoKey struct{}

// withTeamVersionsMemo is ctx sharing one team-versions read per cache
// file; only a run that writes nothing may carry it.
func withTeamVersionsMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, teamVersionsMemoKey{}, &teamVersionsMemo{got: map[*sqlite.Cache]*teamVersionsRead{}})
}

// cacheTeamVersions is c.TeamVersions, read once per cache file under a
// ctx from withTeamVersionsMemo (concurrent callers wait for the one read).
func cacheTeamVersions(ctx context.Context, c *sqlite.Cache) ([]sqlite.TeamVersion, error) {
	m, ok := ctx.Value(teamVersionsMemoKey{}).(*teamVersionsMemo)
	if !ok {
		return c.TeamVersions(ctx)
	}
	m.mu.Lock()
	r, ok := m.got[c]
	if !ok {
		r = &teamVersionsRead{}
		m.got[c] = r
	}
	m.mu.Unlock()
	r.once.Do(func() { r.tvs, r.err = c.TeamVersions(ctx) })
	return r.tvs, r.err
}
