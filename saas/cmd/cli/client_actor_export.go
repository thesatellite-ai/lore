// client_actor_export.go — the .lore/data half of the F18 client copy
// (coordinator ruling 18:40, BUG-D2): in a CLIENT repo of an org, every
// actor row lore exports is written as the projection {id, display name,
// kind}, never stable_key (an email), handle, label or owner_actor_id. The
// live-ref half is live_wire.go (projectActorBytes on diverted rows); both
// use the same projection, and the leak check blocks anything more.
package main

import (
	"context"
	"sync"

	"saas/pkg/aicoder/orgresolve"
)

// clientActorExport returns the lsync ExportDoc hook for the checkout at
// root. Every checkout that is not one of an org's OWN repos gets the
// projection, including a repo where no org resolves: from inside, a
// client's own developer cannot tell that their repo is some org's client
// repo, and the client's CI check must pass on their rows too (BUG-D2,
// lore-scenarios TestF18_ClientRepoActorCopyIsTheProjection). Decided once,
// on the first actor the pass exports. Unknown counts as not own: the
// projection only removes fields, so failing closed costs detail, never a
// leak. lore.db keeps the full row; the roster in the store is untouched
// (store mirrors use their own options, stores_project.go).
func clientActorExport(ctx context.Context, root string) func(table string, b []byte) []byte {
	var once sync.Once
	client := false
	return func(table string, b []byte) []byte {
		if table != actorsTable {
			return b
		}
		once.Do(func() { client = !repoIsOwn(ctx, root) })
		if !client {
			return b
		}
		return projectActorBytes(b)
	}
}

// repoIsOwn reports whether dir resolves to one of an org's own repos (F10
// repo_role own), where the people are the org's and the full actor row may
// be committed.
func repoIsOwn(ctx context.Context, dir string) bool {
	s, err := gatherOrgSituation(ctx, dir)
	if err != nil || s == nil || s.Result.Org == nil {
		return false
	}
	return s.Result.Role == orgresolve.RoleOwn
}
