package wire

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/scan"
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

// liveScanner is the App's scanner: the gitleaks defaults plus the live
// secret_rules rows of every attached store (spec 4.3), rebuilt when those
// rows change. Why: rules are settings rows, and a long-lived App (`lore
// api`) must apply a rule added while it runs to the next write; a scanner
// built once at open would keep scanning with the old set. Refresh (called
// by the engine before each write's scan, engine.RefreshingScanner) reads
// the rule rows and rebuilds only when their ids or HLCs differ, so an
// unchanged set costs one backend read and no regex compile.
//
// Rows are read from the backends, not the engine: the engine cannot
// exist before its scanner. A malformed rule row fails the open or the
// write (E_CONFIG_INVALID, naming the rule id), never silently drops a
// rule. Safe for concurrent use.
//
// Gap (phase 2): every store's rules apply to every write; the per-level
// "stricter wins" resolution of logic/policy is not wired yet.
type liveScanner struct {
	backends map[store.Source]store.Backend
	mu       sync.Mutex
	key      string // the rule rows' ids and HLCs the current scanner was built from
	cur      *gitleaks.Scanner
}

var _ engine.RefreshingScanner = (*liveScanner)(nil)

// newScanner builds the live scanner over backends and its first rule set.
func newScanner(ctx context.Context, backends map[store.Source]store.Backend) (*liveScanner, error) {
	s := &liveScanner{backends: backends}
	if err := s.Refresh(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Refresh implements engine.RefreshingScanner.
func (s *liveScanner) Refresh(ctx context.Context) error {
	rows, key, err := ruleRows(ctx, s.backends)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur != nil && key == s.key {
		return nil
	}
	base, err := gitleaks.Defaults()
	if err != nil {
		return err
	}
	rules, err := gitleaks.Rules(base, rows)
	if err != nil {
		return err
	}
	next, err := gitleaks.New(rules)
	if err != nil {
		return err
	}
	s.cur, s.key = next, key
	return nil
}

// scanner is the current rule set's scanner.
func (s *liveScanner) scanner() *gitleaks.Scanner {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// Scan implements scan.Scanner.
func (s *liveScanner) Scan(field, text string) []scan.Finding { return s.scanner().Scan(field, text) }

// ScanReport implements scan.Reporter (the engine surfaces a degraded scan).
func (s *liveScanner) ScanReport(field, text string) scan.Report {
	return s.scanner().ScanReport(field, text)
}

// ruleRows reads the live secret_rules rows of every backend, with a key
// naming each row's store, id and HLC (sorted), which changes whenever a
// rule is added, edited or deleted.
func ruleRows(ctx context.Context, backends map[store.Source]store.Backend) ([]gitleaks.RuleRow, string, error) {
	var rows []gitleaks.RuleRow
	var keys []string
	for name, b := range backends {
		got, _, readErr := b.Read(ctx, "")
		if readErr != nil {
			return nil, "", readErr
		}
		for _, sr := range got {
			if sr.Table != row.Table(schema.TableSecretRules) {
				continue
			}
			keys = append(keys, string(name)+"/"+sr.ID.String()+"@"+sr.HLC.String())
			if sr.Deleted {
				continue
			}
			r, decErr := sr.Decode()
			if decErr != nil {
				return nil, "", decErr
			}
			rr, ruleErr := ruleRow(r)
			if ruleErr != nil {
				return nil, "", ruleErr
			}
			rows = append(rows, rr)
		}
	}
	slices.Sort(keys)
	// the rules' order must not follow map order either
	slices.SortStableFunc(rows, func(a, b gitleaks.RuleRow) int { return strings.Compare(a.RuleID, b.RuleID) })
	return rows, strings.Join(keys, "\n"), nil
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
