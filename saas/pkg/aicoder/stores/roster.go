package stores

import (
	"context"
	"encoding/json"
	"strings"

	"saas/pkg/aicoder/lorecfg"
)

// actorsDir is the roster's table folder inside the org folder's data.
const actorsDir = "actors/"

// RosterCheck answers "is this agent on the org roster?" (F18) from the
// org folder's actors/ on the store branch, read from the bare clone
// (fetched at most once per FetchTTL). It implements identity.Roster:
// unreadable → (false, false), "not checked", which identity reports.
type RosterCheck struct {
	Manager *Manager
	Org     *lorecfg.Org
}

// rosterActor is the part of an actor file the check reads.
type rosterActor struct {
	StableKey string `json:"stable_key"`
	Active    *bool  `json:"active"`
}

// AgentRegistered implements identity.Roster. A miss on a cached fetch
// refreshes once (a forced fetch) before it answers "not registered": an
// agent started a moment ago by `lore agent start` in another clone is on
// the remote roster but not yet in a fetch younger than FetchTTL, and
// refusing it then made every first claim fail (BUG-T6). A hit, or a miss
// on a fresh fetch, costs no extra fetch.
func (r *RosterCheck) AgentRegistered(stableKey string) (registered, checked bool) {
	if r == nil || r.Org == nil || r.Manager == nil {
		return false, false
	}
	sc := r.Org.OrgFolderStore()
	if sc == nil {
		return false, false
	}
	ctx := context.Background()
	st, err := r.Manager.Open(ctx, sc.URL)
	if err != nil {
		return false, false
	}
	fr, err := st.Fetch(ctx, false)
	if err != nil || (fr.Offline && fr.At.IsZero()) {
		return false, false
	}
	registered, checked = rosterHas(ctx, st, sc, stableKey)
	if registered || !checked || !fr.Cached {
		return registered, checked
	}
	if fr, err = st.Fetch(ctx, true); err != nil || fr.Offline {
		return false, true // the cached roster's answer stands when the refresh cannot reach the remote
	}
	return rosterHas(ctx, st, sc, stableKey)
}

// rosterHas reads the org folder's actors/ at the store branch and reports
// whether an active actor has stableKey; checked is false when the folder
// cannot be read.
func rosterHas(ctx context.Context, st *Store, sc *lorecfg.Store, stableKey string) (registered, checked bool) {
	files, commit, err := st.ReadProject(ctx, sc.EffectiveBranch(), sc.OrgFolder)
	if err != nil || commit == "" {
		return false, false
	}
	for p, b := range files {
		if !strings.HasPrefix(p, actorsDir) {
			continue
		}
		var a rosterActor
		if json.Unmarshal(b, &a) != nil {
			continue
		}
		if a.StableKey == stableKey && (a.Active == nil || *a.Active) {
			return true, true
		}
	}
	return false, true
}
