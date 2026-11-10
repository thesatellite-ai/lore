package wire

// Doctor reads over the machine stamps and profiles (OQ7, AGENT_IDENTITY
// 6.8). Flow:
//
//	TeamVersions      → every attached cache's team_versions (repo + stores)
//	                  → newest save per (actor, machine) across them
//	SharedMachineIDs  → ThisMachine (best effort) → opsinfra.SharedMachineIDs

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
)

// TeamVersions is doctor's "Team versions" view (OQ7): the newest lore
// version each writer saved with on each machine, read from the `_w`
// stamps of every attached cache (the repo and every store), not only the
// repo cache. Code-repo saves carry no machine id (Q-R3-9) and keep
// Machine "". Sorted by actor, then machine.
func (a *App) TeamVersions(ctx context.Context) ([]sqlite.TeamVersion, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	type key struct{ actor, machine string }
	newest := map[key]sqlite.TeamVersion{}
	for _, c := range cacheFiles(st.caches) {
		tvs, terr := c.TeamVersions(ctx)
		if terr != nil {
			return nil, terr
		}
		for _, tv := range tvs {
			k := key{tv.Actor, tv.Machine}
			// RFC 3339 UTC text sorts by time: the newest save wins.
			if cur, ok := newest[k]; !ok || tv.LastSeen > cur.LastSeen {
				newest[k] = tv
			}
		}
	}
	out := make([]sqlite.TeamVersion, 0, len(newest))
	for _, tv := range newest {
		out = append(out, tv)
	}
	slices.SortFunc(out, func(x, y sqlite.TeamVersion) int {
		if c := strings.Compare(x.Actor, y.Actor); c != 0 {
			return c
		}
		return strings.Compare(x.Machine, y.Machine)
	})
	return out, nil
}

// SharedMachineIDs is doctor's shared-machine-id check (6.8): machine ids
// whose profiles name two or more hosts. This machine's own profile joins
// the comparison when it can be read; when it cannot (no machine id here)
// only the cross-store check runs.
func (a *App) SharedMachineIDs(ctx context.Context) ([]opsinfra.SharedMachine, error) {
	me, err := a.ThisMachine(ctx)
	if err != nil {
		me = opsinfra.ThisMachine{} // best effort: the cross-store check still runs
	}
	return opsinfra.SharedMachineIDs(ctx, a, me)
}
