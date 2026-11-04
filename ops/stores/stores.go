// Package stores defines `lore stores sync` (layer L5): exchange the rows
// of the attached 0.1.18 stores (.lore/stores.yaml) with their remotes,
// then refresh their caches.
//
// Why a new noun: the v0.1.17 `lore store` group still owns the old store
// commands (init, add, list, …) and one command word may not have two
// owners (surface/cli.Mount refuses it). When the store noun migrates,
// this operation becomes `store.sync` and the plural noun goes away.
package stores

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// OpSync is the operation name: `lore stores sync`, POST /v1/stores/sync.
const OpSync = "stores.sync"

// Syncer is what the operation needs; the engine provides both methods.
type Syncer interface {
	// Stores lists the attached stores in precedence order.
	Stores() []store.Source
	// Sync exchanges one store's changes with its remote and refreshes its
	// cache (engine.Engine.Sync).
	Sync(ctx context.Context, source store.Source) (store.SyncReport, error)
}

// SyncIn is the input of stores.sync.
type SyncIn struct {
	Store string `json:"store,omitempty" flag:"store" desc:"sync only this store (default every attached store)"`
}

// SyncOut is the output of stores.sync.
type SyncOut struct {
	Stores []Report `json:"stores"`
}

// Report is one store's sync result.
type Report struct {
	Store    string `json:"store"`
	Sent     int    `json:"sent"`
	Received int    `json:"received"`
	// Merged counts rows merged field by field because both sides changed.
	Merged int `json:"merged"`
	// Refused counts rows the sync could not apply (each is listed in
	// Problems with its code); nothing was lost, the local side is kept.
	Refused  int      `json:"refused"`
	Problems []string `json:"problems,omitempty"`
	Offline  bool     `json:"offline,omitempty"`
}

// refusedErr turns refused rows into E_STALE so a script or agent sees a
// non-zero exit: the sync did not finish. Both sides are kept (the local
// commits stay local, the remote is untouched), so nothing is lost.
func refusedErr(out SyncOut) error {
	var problems []string
	for _, r := range out.Stores {
		for _, p := range r.Problems {
			problems = append(problems, r.Store+": "+p)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return errs.New(errs.Stale, "stores sync: "+strings.Join(problems, "; ")).
		WithHint("both edits are kept; settle the row on one side (lore memory show <id> on each), then sync again")
}

// Ops returns the stores operations bound to s.
func Ops(s Syncer) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpSync, Summary: "Sync the attached stores (.lore/stores.yaml) with their remotes",
			Permission: op.PermWrite, Access: op.AccessRead,
			Errors:   []errs.Code{errs.NotFound, errs.Stale, errs.Contended, errs.StoreLocked, errs.StoreUnreachable, errs.PolicyDenied, errs.Internal},
			Examples: []op.Example{{Title: "sync every store", CLI: "lore stores sync", Want: "stores:"}},
		}, func(ctx context.Context, in SyncIn) (SyncOut, error) {
			names := s.Stores()
			if in.Store != "" {
				names = []store.Source{store.Source(in.Store)}
			}
			out := SyncOut{Stores: []Report{}}
			for _, n := range names {
				rep, err := s.Sync(ctx, n)
				if err != nil {
					return SyncOut{}, errs.Wrap(errs.CodeOf(err), "stores sync: "+string(n), err)
				}
				r := Report{Store: string(n), Sent: rep.Sent, Received: rep.Received, Merged: len(rep.Merged),
					Refused: len(rep.Refused), Offline: rep.Offline}
				for _, x := range rep.Refused {
					r.Problems = append(r.Problems, string(x.Code)+" "+x.ID.String()+": "+x.Reason)
				}
				out.Stores = append(out.Stores, r)
			}
			return out, refusedErr(out)
		}),
	}
}
