package wire

// branchlayer_sweep.go — what `lore sync` does for this repo's branch
// layers after the stores synced (BRANCH_LAYERS 9, 10): a layer whose
// code branch merged lands when branches.auto_land is true (otherwise
// `lore branch list` offers it), and a layer whose code branch was deleted
// without merging moves to the trash once its last write is older than
// branches.orphan_days. Only `lore sync` does this: a read changes
// nothing, and an offline sync never trashes (it cannot tell "deleted"
// from "not fetched").

import (
	"context"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/logic/policy"
	opsbranch "github.com/thesatellite-ai/lore/ops/branch"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// defaultOrphanDays is branches.orphan_days when no level sets it.
const defaultOrphanDays = 14

// sweepLayers runs the after-sync step and returns the lines it prints.
func (a *App) sweepLayers(ctx context.Context) []string {
	st, lb, name, err := a.branchTarget(ctx)

	if err != nil || lb == nil {
		return nil
	}
	set, err := st.eng.Policy(withoutLayers(ctx), name)
	if err != nil {
		return nil
	}
	days := int64(defaultOrphanDays)
	if v, ok := set.Limit(policy.KeyBranchesOrphanDays); ok {
		days = v
	}
	autoLand, _ := set.Flag(policy.KeyBranchesAutoLand)
	entries, err := lb.gb.LayerList(ctx, lb.key.RepoKey, false)
	if err != nil {
		return nil
	}

	now := a.opts.Wall()
	var lines []string
	for _, e := range entries {
		state, _ := codeState(ctx, lb, e.Layer)
		switch state {
		case opsbranch.StateMerged:
			if !autoLand {
				continue
			}
			out, lerr := landLayer(ctx, lb.gb, e.Layer.Key, lb.code.Remote, opsbranch.LandIn{Branch: e.Layer.Key.Branch})
			if lerr != nil {
				lines = append(lines, "branch notes of "+e.Layer.Key.Branch+" not landed: "+lerr.Error())
				continue
			}
			lines = append(lines, "branches.auto_land: "+out.Text())
		case opsbranch.StateOrphaned:
			last, terr := lb.gb.LayerTipTime(ctx, e.Layer)
			if terr != nil || now.Sub(last) < time.Duration(days)*24*time.Hour {
				continue
			}
			trash, derr := lb.gb.LayerTrash(ctx, e.Layer.Key, branchlayer.StateDropped, "", true)
			if derr != nil {
				lines = append(lines, "branch notes of "+e.Layer.Key.Branch+" not moved to the trash: "+derr.Error())
				continue
			}
			lines = append(lines, "lore: "+e.Layer.Key.Branch+" was deleted without merging and its notes waited "+strconv.FormatInt(days, 10)+" days: moved them to the trash ("+trash+"); lore branch restore --branch "+e.Layer.Key.Branch+" brings them back")
		}
	}
	return lines
}

// syncWasOffline reports whether any store of the sync could not reach
// its remote: the sweep then waits for the next sync.
func syncWasOffline(out opsstores.SyncOut) bool {
	for _, r := range out.Stores {
		if r.Offline {
			return true
		}
	}
	return false
}
