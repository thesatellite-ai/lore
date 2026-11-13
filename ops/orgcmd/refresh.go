package orgcmd

// refresh.go — `lore org refresh <org>` (SOURCE_OF_TRUTH row 9): the org
// entry in ~/.lore/config.yaml copies stores, own, repos and defaults
// from the store's lore-org.yaml (the source) and records which version
// it copied.
// Every open refreshes a stale entry by itself after a fetch; this verb is
// the doctor's fix (org_source) and the way to adopt a suggestion an entry
// changed by hand no longer matches.
//
//	org.refresh ─▶ Host upgraded to Refresher? ─ no ─▶ E_UNSUPPORTED
//	            ─▶ fetch the org store ─▶ read lore-org.yaml ─▶ copy +
//	               stamp (`source: {store, version}`) ─▶ Report

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// OpRefresh copies an org entry's shared fields from its store.
const OpRefresh = "org.refresh"

// RefreshIn is the input of org.refresh.
type RefreshIn struct {
	Org string `json:"org,omitempty" arg:"0" desc:"the org entry to refresh from its store's lore-org.yaml (default: the only org entry on this machine, else the active setup's org)"`
}

// Refresher is the optional Host upgrade behind org.refresh.
type Refresher interface {
	Refresh(ctx context.Context, in RefreshIn) (Report, error)
}

// refreshOp is org.refresh bound to o.
func refreshOp(o ops, spec func(name, summary string, errCodes []errs.Code, mayAsk bool, ex ...op.Example) op.Spec, codes []errs.Code) op.Entry {
	return op.Define(spec(OpRefresh, "Copy an org entry's stores, own, repos and defaults from the store's lore-org.yaml (the team's source) and record its version", codes, false,
		op.Example{Title: "the store's suggestion changed", CLI: "lore org refresh --org agency", Want: "agency"},
		op.Example{Title: "the only org on this machine (or the active setup's)", CLI: "lore org refresh", Want: "agency"}),
		func(ctx context.Context, in RefreshIn) (Report, error) {
			h, err := o.host(ctx, OpRefresh)
			if err != nil {
				return Report{}, err
			}
			r, ok := upgrade.As[Refresher](h)
			if !ok {
				return Report{}, errs.New(errs.Unsupported, "lore org refresh needs the org store machinery, which this binary does not have yet").
					WithHint("run it with the release binary")
			}
			return withDefaultOrg(ctx, h, OpRefresh, &in.Org, func() (Report, error) { return r.Refresh(ctx, in) })
		})
}
