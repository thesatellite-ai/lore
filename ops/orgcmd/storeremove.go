package orgcmd

// storeremove.go — `lore org store remove` (devco T-22): a client
// contract ends and its store is retired from the org. Before this verb
// `lore org store` had only `add`: the ended client's store stayed in the
// org's store list for every member, cloned and fetched by their reads,
// and the only way out was a hand edit of lore-org.yaml in the store
// clone that no message named.
//
//	org.store.remove ─▶ Host upgraded to StoreRemover? ─ no ─▶ E_UNSUPPORTED
//	                 ─▶ admin of the org? ─ no ─▶ E_REFUSED
//	                 ─▶ plan (writes nothing) ─▶ not confirmed: E_NEEDS_CONFIRM
//	                 ─▶ lore-org.yaml without the store, committed in the
//	                    org store's clone (pushed only with --push)
//	                 ─▶ this machine's org entry copies it (org refresh)
//
// The store's remote and every note in it stay: export them first with
// `lore task export` / `lore memory list --json`, or keep reading them by
// attaching the store by hand (`lore store add`).

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// OpStoreRemove retires a store from an org's lore-org.yaml and this
// machine's entry.
const OpStoreRemove = "org.store.remove"

// StoreRemoveIn is the input of org.store.remove.
type StoreRemoveIn struct {
	Org  string `json:"org,omitempty" flag:"org" desc:"the org entry (default: the only org entry on this machine, else the active setup's org)"`
	URL  string `json:"url" flag:"url" desc:"the store to retire: its git url (required; lore store list names the org's stores)"`
	Push bool   `json:"push,omitempty" flag:"push" desc:"push the lore-org.yaml commit to the org store now (default: commit in the store clone only; the next write to that store sends it)"`
}

// StoreRemover is the optional Host upgrade behind org.store.remove.
type StoreRemover interface {
	RemoveStore(ctx context.Context, in StoreRemoveIn, confirmed bool) (Report, error)
}

// StoreRemoveCommand is the command a hint names to retire url from org.
func StoreRemoveCommand(org, url string) string {
	return "lore org store remove --org " + org + " --url " + url
}

// storeRemoveOp is org.store.remove bound to o.
func storeRemoveOp(o ops, spec func(name, summary string, errCodes []errs.Code, mayAsk bool, ex ...op.Example) op.Spec, codes []errs.Code) op.Entry {
	return op.Define(spec(OpStoreRemove, "Retire a store from an org (admin only): drop it from its lore-org.yaml so members stop cloning and fetching it; the store and its notes are kept", codes, true,
		op.Example{Title: "a client contract ended", CLI: "lore org store remove --org agency --url git@github.com:agency/client-notes.git --push --yes", Want: "retired store"}),
		func(ctx context.Context, in StoreRemoveIn) (Report, error) {
			if strings.TrimSpace(in.URL) == "" {
				return Report{}, errs.New(errs.InvalidInput, "name the store to retire with --url <store url>").
					WithHint("lore store list names the org's stores (SOURCE org); then: lore org store remove --org <org> --url <url>")
			}
			return o.ask(ctx, OpStoreRemove, func(h Host, yes bool) (Report, error) {
				r, ok := upgrade.As[StoreRemover](h)
				if !ok {
					return Report{}, errs.New(errs.Unsupported, "lore org store remove needs the org store machinery, which this binary does not have yet").
						WithHint("run it with the release binary")
				}
				return withDefaultOrg(ctx, h, OpStoreRemove, &in.Org, func() (Report, error) { return r.RemoveStore(ctx, in, yes) })
			})
		})
}
