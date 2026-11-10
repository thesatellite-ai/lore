package metalist

// alias.go — meta.scope_aliases for every --meta argument the ops read
// (METADATA 3.6, FA-META-12):
//
//	ScopeAliases(env) ─ env.Policy(repo) ─ Set.ScopeAliases ─ alias → scope
//	Select / meta set ─ meta.ExpandAliases(args, aliases) ─ meta.Parse

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/store"
)

// policier is the optional Env upgrade that reads the effective policy
// (wire.App has it; same shape as bulk.Policier).
type policier interface {
	Policy(ctx context.Context, target store.Source) (policy.Set, error)
}

// ScopeAliases is the repo chain's meta.scope_aliases (alias → scope
// label); nil when env reads no policy. Conflicting aliases are left out
// (policy.Set.ScopeAliases), so the plain form is the only one that works.
func ScopeAliases[E any](ctx context.Context, env E) (map[string]string, error) {
	p, ok := upgrade.As[policier](env)
	if !ok {
		return nil, nil
	}
	set, err := p.Policy(ctx, store.SourceRepo)
	if err != nil {
		return nil, err
	}
	aliases, _ := set.ScopeAliases()
	return aliases, nil
}
