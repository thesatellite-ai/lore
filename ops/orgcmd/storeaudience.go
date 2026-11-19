package orgcmd

// storeaudience.go — `lore org store audience` (devco D-442): mark a store
// of the org as used only by its admins (servers, domains, accounts), or
// open it to every member again. Before it, an admins-only store sat in
// lore-org.yaml like any other: every member's lore cloned it, failed,
// and treated their own repos as client repos; the only way out was a
// second org.
//
//	org.store.audience ─▶ Host upgraded to StoreAudiencer? ─ no ─▶ E_UNSUPPORTED
//	                   ─▶ admin of the org? ─ no ─▶ E_REFUSED
//	                   ─▶ the org folder's store? ─ yes ─▶ E_REFUSED
//	                   ─▶ plan (writes nothing) ─▶ not confirmed: E_NEEDS_CONFIRM
//	                   ─▶ lore-org.yaml's store gets audience: admins (or
//	                      loses the key), committed in the org store's
//	                      clone (pushed only with --push)
//	                   ─▶ this machine's org entry copies it

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// OpStoreAudience sets who in an org uses one of its stores.
const OpStoreAudience = "org.store.audience"

// StoreAudienceIn is the input of org.store.audience.
type StoreAudienceIn struct {
	Org      string `json:"org,omitempty" flag:"org" desc:"the org entry (default: the only org entry on this machine, else the active setup's org)"`
	URL      string `json:"url" flag:"url" desc:"the store: its git url (required; lore store list names the org's stores)"`
	Audience string `json:"audience" flag:"audience" desc:"who uses the store: admins (only the org's admins; members' lore never reads it) or everyone (required)"`
	Push     bool   `json:"push,omitempty" flag:"push" desc:"push the lore-org.yaml commit to the org store now (default: commit in the store clone only; lore sync, or lore store push --store <org>, sends it)"`
}

// StoreAudiencer is the optional Host upgrade behind org.store.audience.
type StoreAudiencer interface {
	SetStoreAudience(ctx context.Context, in StoreAudienceIn, confirmed bool) (Report, error)
}

// StoreAudienceCommand is the command a hint names to set url's audience.
func StoreAudienceCommand(org, url string, a orgconf.Audience) string {
	return "lore org store audience --org " + org + " --url " + url + " --audience " + a.String()
}

// storeAudienceOp is org.store.audience bound to o.
func storeAudienceOp(o ops, spec func(name, summary string, errCodes []errs.Code, mayAsk bool, ex ...op.Example) op.Spec, codes []errs.Code) op.Entry {
	return op.Define(spec(OpStoreAudience, "Set who in an org uses one of its stores (admin only): admins (members' lore never reads it) or everyone", codes, true,
		op.Example{Title: "keep the servers store to admins", CLI: "lore org store audience --org agency --url git@github.com:agency/ops.git --audience admins --push --yes", Want: "admins"},
		op.Example{Title: "open it to every member again", CLI: "lore org store audience --org agency --url git@github.com:agency/ops.git --audience everyone --push --yes", Want: "everyone"}),
		func(ctx context.Context, in StoreAudienceIn) (Report, error) {
			if strings.TrimSpace(in.URL) == "" {
				return Report{}, errs.New(errs.InvalidInput, "name the store with --url <store url>").
					WithHint("lore store list names the org's stores (SOURCE org); then: lore org store audience --org <org> --url <url> --audience admins")
			}
			if strings.TrimSpace(in.Audience) == "" {
				return Report{}, errs.New(errs.InvalidInput, "say who uses the store with --audience admins or --audience everyone").
					WithHint("lore org store audience --org <org> --url " + in.URL + " --audience admins")
			}
			if _, err := orgconf.ParseAudience(in.Audience); err != nil {
				return Report{}, errs.Wrap(errs.InvalidInput, "--audience "+in.Audience, err).
					WithHint("lore org store audience --org <org> --url " + in.URL + " --audience admins")
			}
			return o.ask(ctx, OpStoreAudience, func(h Host, yes bool) (Report, error) {
				a, ok := upgrade.As[StoreAudiencer](h)
				if !ok {
					return Report{}, errs.New(errs.Unsupported, "lore org store audience needs the org store machinery, which this binary does not have yet").
						WithHint("run it with the release binary")
				}
				return withDefaultOrg(ctx, h, OpStoreAudience, &in.Org, func() (Report, error) { return a.SetStoreAudience(ctx, in, yes) })
			})
		})
}
