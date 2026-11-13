package orgcmd

// storeadd.go — `lore org store add` (B-4): add a store to an existing
// org. An org's stores live in its lore-org.yaml (the team's source, in
// the org folder's store); before this verb an admin had to edit that file
// in the store clone by hand, git commit and push it, then run `lore org
// refresh`, and `lore org link --store <new>` refused with only the old
// stores as its hint.
//
//	org.store.add ─▶ Host upgraded to StoreAdder? ─ no ─▶ E_UNSUPPORTED
//	              ─▶ plan (writes nothing) ─▶ not confirmed: E_NEEDS_CONFIRM
//	              ─▶ lore-org.yaml + the store entry, committed in the
//	                 org store's clone (pushed only with --push)
//	              ─▶ this machine's org entry copies it (org refresh)

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// OpStoreAdd adds a store to an org's lore-org.yaml and this machine's
// entry.
const OpStoreAdd = "org.store.add"

// StoreAddIn is the input of org.store.add.
type StoreAddIn struct {
	Org    string `json:"org,omitempty" flag:"org" desc:"the org entry (default: the only org entry on this machine, else the active setup's org)"`
	URL    string `json:"url" flag:"url" desc:"the store to add: its git url (required)"`
	Name   string `json:"name,omitempty" flag:"name" desc:"the store's short name (default: the repo name of --url)"`
	Branch string `json:"branch,omitempty" flag:"branch" desc:"the store branch (default: the store's default branch)"`
	Push   bool   `json:"push,omitempty" flag:"push" desc:"push the lore-org.yaml commit to the org store now (default: commit in the store clone only; the next write to that store sends it)"`
}

// StoreAdder is the optional Host upgrade behind org.store.add (an
// optional interface so a Host written before it keeps compiling).
type StoreAdder interface {
	AddStore(ctx context.Context, in StoreAddIn, confirmed bool) (Report, error)
}

// StoreAddCommand is the command a hint names to add a store to org.
func StoreAddCommand(org, url string) string {
	return "lore org store add --org " + org + " --url " + url
}

// storeAddOp is org.store.add bound to o.
func storeAddOp(o ops, spec func(name, summary string, errCodes []errs.Code, mayAsk bool, ex ...op.Example) op.Spec, codes []errs.Code) op.Entry {
	return op.Define(spec(OpStoreAdd, "Add a store to an org: its lore-org.yaml (committed in the org store's clone; --push sends it) and this machine's org entry", codes, true,
		op.Example{Title: "a store for one client's notes", CLI: "lore org store add --org agency --url git@github.com:agency/client-notes.git --yes", Want: "added store"},
		op.Example{Title: "and push it to the team now", CLI: "lore org store add --org agency --url git@github.com:agency/client-notes.git --push --yes", Want: "pushed"}),
		func(ctx context.Context, in StoreAddIn) (Report, error) {
			if strings.TrimSpace(in.URL) == "" {
				return Report{}, errs.New(errs.InvalidInput, "name the store to add with --url <store url>").
					WithHint("make the store first with `lore store init --name <name> --url <url>`, then: lore org store add --org <org> --url <url>")
			}
			return o.ask(ctx, OpStoreAdd, func(h Host, yes bool) (Report, error) {
				a, ok := upgrade.As[StoreAdder](h)
				if !ok {
					return Report{}, errs.New(errs.Unsupported, "lore org store add needs the org store machinery, which this binary does not have yet").
						WithHint("run it with the release binary")
				}
				return withDefaultOrg(ctx, h, OpStoreAdd, &in.Org, func() (Report, error) { return a.AddStore(ctx, in, yes) })
			})
		})
}
