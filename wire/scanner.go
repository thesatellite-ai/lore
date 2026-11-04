package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// secret_rules field names (schema.Builtin).
const (
	ruleFieldID       = "rule_id"
	ruleFieldRegex    = "regex"
	ruleFieldKeywords = "keywords"
	ruleFieldEntropy  = "entropy"
	ruleFieldState    = "state"
)

// newScanner builds the gitleaks scanner from its default rules plus the
// live secret_rules rows of every attached store (spec 4.3). Rows are read
// from the backends, not the engine: the engine cannot exist before its
// scanner. A malformed rule row fails the open (E_CONFIG_INVALID, naming
// the rule id), never silently drops a rule.
//
// Gap (phase 2): every store's rules apply to every write; the per-level
// "stricter wins" resolution of logic/policy is not wired yet.
func newScanner(ctx context.Context, backends map[store.Source]store.Backend) (*gitleaks.Scanner, error) {
	base, err := gitleaks.Defaults()
	if err != nil {
		return nil, err
	}
	var rows []gitleaks.RuleRow
	for _, b := range backends {
		got, _, readErr := b.Read(ctx, "")
		if readErr != nil {
			return nil, readErr
		}
		for _, sr := range got {
			if sr.Table != row.Table(schema.TableSecretRules) || sr.Deleted {
				continue
			}
			r, decErr := sr.Decode()
			if decErr != nil {
				return nil, decErr
			}
			rr, ruleErr := ruleRow(r)
			if ruleErr != nil {
				return nil, ruleErr
			}
			rows = append(rows, rr)
		}
	}
	rules, err := gitleaks.Rules(base, rows)
	if err != nil {
		return nil, err
	}
	return gitleaks.New(rules)
}

// ruleRow maps one secret_rules row to the scanner's input.
func ruleRow(r row.Row) (gitleaks.RuleRow, error) {
	rr := gitleaks.RuleRow{
		RuleID: textField(r, ruleFieldID), Regex: textField(r, ruleFieldRegex), State: textField(r, ruleFieldState),
	}
	// any form: a List, or a JSON array as a row file may decode
	rr.Keywords = value.ItemStrings(r.Fields[ruleFieldKeywords])
	if n, ok := r.Fields[ruleFieldEntropy].(value.Number); ok {
		f, err := n.Float64()
		if err != nil {
			return gitleaks.RuleRow{}, errs.Wrap(errs.ConfigInvalid, "wire: secret rule "+rr.RuleID+": bad entropy", err)
		}
		rr.Entropy = f
	}
	return rr, nil
}

// textField returns a field's text ("" when absent).
func textField(r row.Row, name string) string {
	if v, ok := r.Fields[name]; ok && v.Kind() == value.KindText {
		return v.String()
	}
	return ""
}
