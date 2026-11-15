package stores

// `lore store trust --name <store>` (devco R-9): a store that the repo's
// committed .lore/stores.yaml attaches, on a host outside the org's own:
// and repos: patterns, that nobody attached on this machine, is not
// cloned or read (its notes would reach the agent's context, and anyone
// who can push to the repo can add such a line). lore names it on every
// sync and doctor; this command is the person's yes, recorded on this
// machine only (~/.lore/state/trusted-stores.json), after which the store
// is cloned and read like any other.

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// OpTrust records the person's yes to a store a committed stores.yaml attaches.
const OpTrust = "store.trust"

// TrustIn is the input of store.trust.
type TrustIn struct {
	Name string `json:"name,omitempty" flag:"name" desc:"the store, by its name in .lore/stores.yaml (lore sync names the ones waiting)"`
}

// TrustOut is the output of store.trust.
type TrustOut struct {
	// Store is the store's name in stores.yaml; URL its url (no credentials).
	Store string `json:"store"`
	URL   string `json:"url"`
	// Changed is false when the store was trusted already.
	Changed bool `json:"changed"`
}

// Truster is the optional Manager upgrade behind store.trust.
type Truster interface {
	TrustStore(ctx context.Context, in TrustIn) (TrustOut, error)
}

// Text is the result and what it means.
func (o TrustOut) Text() string {
	if !o.Changed {
		return fmt.Sprintf("store %s (%s) is trusted on this machine already; nothing changed\n", o.Store, o.URL)
	}
	return fmt.Sprintf("✓ store %s (%s) is trusted on this machine: the next lore command clones and reads it (recorded in ~/.lore/state/trusted-stores.json)\n", o.Store, o.URL)
}

// trustErrors are the codes of store.trust.
var trustErrors = []errs.Code{errs.InvalidInput, errs.NotFound, errs.NotAStore, errs.Internal}

// trustOp is store.trust bound to m.
func trustOp(m Manager) op.Entry {
	const verb = "store trust"
	return op.Define(op.Spec{
		Name: OpTrust, Summary: "Trust a store the repo's .lore/stores.yaml attaches on a host outside your org, so lore clones and reads it (this machine only)",
		Permission: op.PermWrite, Access: op.AccessRead, Errors: trustErrors,
		Examples: []op.Example{
			{Title: "trust a store a teammate attached", CLI: "lore store trust --name notes", Want: "is trusted on this machine"},
		},
	}, func(ctx context.Context, in TrustIn) (TrustOut, error) {
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" {
			return TrustOut{}, errs.New(errs.InvalidInput, verb+": name the store; usage: lore store trust --name <store>").
				WithHint("lore sync names the stores waiting for your yes")
		}
		t, ok := upgrade.As[Truster](m)
		if !ok {
			return TrustOut{}, errs.New(errs.Internal, verb+": this build cannot trust a store")
		}
		return t.TrustStore(ctx, in)
	})
}
