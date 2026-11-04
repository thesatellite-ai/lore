package engine

import (
	"context"
	"math"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// secret_allows field names (schema.Builtin).
const (
	allowRowID = "row_id"
	allowField = "field"
	allowRule  = "rule_id"
	allowHash  = "match_hash"
)

// storeSettings is the engine's default Settings: the policies,
// secret_rules and secret_allows rows of EVERY attached store, each counted
// at its store's level (Attached.Level), resolved by logic/policy
// (stricter wins for policies; a rule off at any level is off, a personal
// off only for own captures; D2/D9, BATCH 8). Every store the repo sees
// takes part, so a store's stricter policy can never be skipped.
//
// It reads the backends on every call: settings rows are few, and a cache
// would be hidden state that could disagree with what is stored.
type storeSettings struct{ e *Engine }

// levelOf is the settings level of an attached store.
func levelOf(a Attached) policy.Level {
	switch {
	case a.Level != "":
		return a.Level
	case a.Name == store.SourceRepo:
		return policy.LevelRepo
	}
	return policy.LevelStoreRoot
}

// settingsRows reads the live rows of table in every attached store with
// their levels, in precedence order.
func (s storeSettings) settingsRows(table row.Table) ([]policy.Leveled[row.Row], error) {
	ctx := context.Background()
	var out []policy.Leveled[row.Row]
	for _, name := range s.e.order {
		a := s.e.stores[name]
		rows, err := s.e.List(ctx, name, table)
		if err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "engine: read the "+string(table)+" rows of store "+string(name), err)
		}
		for _, r := range rows {
			out = append(out, policy.Leveled[row.Row]{Level: levelOf(a), Source: r.ID, Value: r})
		}
	}
	return out, nil
}

// Policy implements Settings.
func (s storeSettings) Policy(store.Source) (policy.Set, policy.Rules, error) {
	pols, err := s.settingsRows(schema.TablePolicies)
	if err != nil {
		return policy.Set{}, policy.Rules{}, err
	}
	entries := make([]policy.Entry, 0, len(pols))
	for _, p := range pols {
		en, err := policy.EntryFromRow(p.Level, p.Value)
		if err != nil {
			return policy.Set{}, policy.Rules{}, err
		}
		entries = append(entries, en)
	}
	set, err := policy.Resolve(entries)
	if err != nil {
		return policy.Set{}, policy.Rules{}, err
	}
	rs, err := s.settingsRows(schema.TableSecretRules)
	if err != nil {
		return policy.Set{}, policy.Rules{}, err
	}
	rules := make([]policy.RuleEntry, 0, len(rs))
	for _, r := range rs {
		re, err := policy.RuleEntryFromRow(r.Level, r.Value)
		if err != nil {
			return policy.Set{}, policy.Rules{}, err
		}
		rules = append(rules, re)
	}
	return set, policy.ResolveRules(rules), nil
}

// Policy is the effective policy set a write to target sees: the same
// Settings source the write path reads (WithSettings, else every attached
// store's policies rows at their levels). A malformed row fails closed
// (E_CONFIG_INVALID naming it).
func (e *Engine) Policy(target store.Source) (policy.Set, error) {
	set, _, err := e.settings.Policy(target)
	return set, err
}

// Allows implements Settings: every secret_allows row of every store. A
// malformed row fails closed (E_CONFIG_INVALID naming it).
func (s storeSettings) Allows(store.Source) ([]scan.Allow, error) {
	rs, err := s.settingsRows(schema.TableSecretAllows)
	if err != nil {
		return nil, err
	}
	out := make([]scan.Allow, 0, len(rs))
	for _, l := range rs {
		a, err := allowOf(l.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// allowOf reads one secret_allows row.
func allowOf(r row.Row) (scan.Allow, error) {
	bad := func(why string) error {
		return errs.New(errs.ConfigInvalid, "engine: secret_allows row "+r.ID.String()+" "+why)
	}
	target, err := ids.ParseAny(textOf(r, allowRowID))
	if err != nil {
		return scan.Allow{}, bad("has no valid row_id")
	}
	n, ok := r.Fields[allowHash].(value.Number)
	if !ok {
		return scan.Allow{}, bad("has no match_hash")
	}
	h, err := n.Int64()
	if err != nil || h < 0 || h > math.MaxUint32 {
		return scan.Allow{}, bad("has a match_hash out of range")
	}
	return scan.Allow{RowID: target, Field: textOf(r, allowField), RuleID: textOf(r, allowRule), MatchHash: uint32(h)}, nil
}

// textOf is a text field of r ("" when absent).
func textOf(r row.Row, f string) string {
	if t, ok := r.Fields[f].(value.Text); ok {
		return t.String()
	}
	return ""
}
