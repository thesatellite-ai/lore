package settings

import (
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// Item checks of the set-shaped policy keys (b-errs): a set key's value is
// a list, and each key's items have their own domain, so `policy set
// agents.person_only true` (a flag word, no operation) is refused instead
// of stored as a rule that never matches.
//
//	agents.person_only       an operation name (task.done), or <operation>.<input field> (meta.edit.merge_into, AGENT_IDENTITY 8.2)
//	routing.client_repos     <kind>=<target>, both sides non-empty
//	meta.scope_aliases       <alias>=<scope label>, both sides non-empty
//	context.targets          checked by checkContextTargets (tool profiles)

// registrar is the optional Env upgrade that exposes the operation
// registry (wire.App); without it agents.person_only items are only
// checked for shape.
type registrar interface {
	Registry() *op.Registry
}

// pairSepPolicy splits a key=value set item.
const pairSepPolicy = "="

// checkSetItems refuses an item outside key d's domain.
func (o ops) checkSetItems(d policy.KeyDef, items []string) error {
	switch d.Key {
	case policy.KeyAgentsPersonOnly:
		reg, ok := upgrade.As[registrar](o.env)
		for _, it := range items {
			if !strings.Contains(it, ".") {
				return personOnlyItemErr(it)
			}
			if !ok || reg.Registry() == nil {
				continue
			}
			if !knownOpItem(reg.Registry(), it) {
				return personOnlyItemErr(it)
			}
		}
	case policy.KeyClientRoutes, policy.KeyMetaScopeAliases:
		for _, it := range items {
			l, r, cut := strings.Cut(it, pairSepPolicy)
			if !cut || strings.TrimSpace(l) == "" || strings.TrimSpace(r) == "" {
				return errs.New(errs.InvalidInput, "policy set "+string(d.Key)+": item "+quote(it)+" is not <name>=<value>").
					WithHint("for example " + map[policy.Key]string{policy.KeyClientRoutes: "rule=agency", policy.KeyMetaScopeAliases: "be=repo:acmehub_backend"}[d.Key])
			}
		}
	default:
		// every other key's value is checked by its KeyDef shape alone
	}
	return nil
}

// knownOpItem: it is a registered operation, or one followed by
// ".<input field>" of that operation.
func knownOpItem(reg *op.Registry, it string) bool {
	if _, ok := reg.Lookup(it); ok {
		return true
	}
	i := strings.LastIndex(it, ".")
	e, ok := reg.Lookup(it[:i])
	if !ok {
		return false
	}
	for _, f := range e.InputFields() {
		if f.Name == it[i+1:] {
			return true
		}
	}
	return false
}

// personOnlyItemErr is the refusal of an agents.person_only item.
func personOnlyItemErr(it string) error {
	return errs.New(errs.InvalidInput, "policy set agents.person_only: "+quote(it)+" is not an operation name").
		WithHint("list operations as noun.verb (task.done, meta.edit) or an operation and one input field (meta.edit.merge_into); lore help lists the commands")
}
