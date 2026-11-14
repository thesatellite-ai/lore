package gitleaks

import (
	"strings"
	"sync"

	"github.com/spf13/viper"
	glconfig "github.com/zricethezav/gitleaks/v8/config"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// RuleRow is one secret_rules row (spec 4.3) as the scanner reads it. The
// engine maps store rows to it; this package never reads a store.
type RuleRow struct {
	RuleID   string
	Regex    string
	Keywords []string
	Entropy  float64
	// D2/D9: users fully control rules, lore's and gitleaks' defaults
	// included, so "off" may remove any rule by id.
	// State is one of schema.SecretRuleStates(): "on" adds a new rule (or
	// keeps a default), "off" removes a rule, "changed" replaces a rule's
	// regex/keywords/entropy (or adds one when the id is new).
	State string
}

// gitleaksDefaults is gitleaks' default config, translated once. It is the
// only package-level state and is never mutated after the first load.
// A private viper instance is used: gitleaks' own NewDetectorDefaultConfig
// writes viper's global, which is not safe beside other viper users.
var gitleaksDefaults = sync.OnceValues(func() (glconfig.Config, error) {
	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(strings.NewReader(glconfig.DefaultConfig)); err != nil {
		return glconfig.Config{}, errs.Wrap(errs.Internal, "gitleaks: read default config", err)
	}
	var vc glconfig.ViperConfig
	if err := v.Unmarshal(&vc); err != nil {
		return glconfig.Config{}, errs.Wrap(errs.Internal, "gitleaks: decode default config", err)
	}
	cfg, err := vc.Translate()
	if err != nil {
		return glconfig.Config{}, errs.Wrap(errs.Internal, "gitleaks: translate default config", err)
	}
	return cfg, nil
})

// Warm starts loading gitleaks' default config on another goroutine and
// returns at once; Defaults then waits for it or finds it done. A write
// command calls it before it queues for the live lock, so the ~30 ms of
// translating some 200 rules runs while the command waits instead of while
// it holds the lock and every other agent of the clone waits (D-159).
func Warm() {
	go func() { _, _ = gitleaksDefaults() }()
}

// Defaults is the base rule set: lore's pack first, then every gitleaks
// default rule in gitleaks' own order. Gitleaks rules are described here
// (id, regex text, keywords, entropy) but run on gitleaks' engine, which
// also applies their secret groups and allowlists.
func Defaults() ([]scan.Rule, error) {
	cfg, err := gitleaksDefaults()
	if err != nil {
		return nil, err
	}
	out := Pack()
	for _, id := range cfg.OrderedRules {
		r := cfg.Rules[id]
		re := ""
		if r.Regex != nil {
			re = r.Regex.String()
		}
		out = append(out, scan.Rule{
			ID: r.RuleID, Description: r.Description, Regex: re,
			Keywords: append([]string(nil), r.Keywords...), Entropy: r.Entropy, Source: scan.SourceGitleaks,
		})
	}
	return out, nil
}

// Rules applies secret_rules rows to base and returns the effective rule
// set; base is not modified. Pure: same inputs, same output, order kept
// (base order, then new rules in row order).
//
// Refused (E_CONFIG_INVALID, naming the rule id, never its regex text):
// an unknown state, two rows for one id, "changed" or a new "on" rule
// without a regex, and "on" with a regex for an id that already exists
// (that is a change: use "changed"). "off" for an unknown id is a no-op
// so a team row can switch off a rule a given binary does not ship.
func Rules(base []scan.Rule, rows []RuleRow) ([]scan.Rule, error) {
	byID := map[string]RuleRow{}
	for _, r := range rows {
		if r.RuleID == "" {
			return nil, errs.New(errs.ConfigInvalid, "secret rule: a row has no rule_id")
		}
		if !validState(r.State) {
			return nil, errs.New(errs.ConfigInvalid, "secret rule "+r.RuleID+": unknown state")
		}
		if _, dup := byID[r.RuleID]; dup {
			return nil, errs.New(errs.ConfigInvalid, "secret rule "+r.RuleID+": more than one row")
		}
		byID[r.RuleID] = r
	}
	seen := map[string]bool{}
	out := make([]scan.Rule, 0, len(base)+len(rows))
	for _, b := range base {
		seen[b.ID] = true
		row, ok := byID[b.ID]
		switch {
		case !ok:
			out = append(out, b)
		case row.State == schema.SecretRuleOff:
		case row.State == schema.SecretRuleChanged:
			if row.Regex == "" {
				return nil, errs.New(errs.ConfigInvalid, "secret rule "+row.RuleID+": changed without a regex")
			}
			out = append(out, fromRow(row, b.Description))
		default: // on
			if row.Regex != "" {
				return nil, errs.New(errs.ConfigInvalid, "secret rule "+row.RuleID+": already exists, use state changed to replace it")
			}
			out = append(out, b)
		}
	}
	for _, r := range rows {
		if seen[r.RuleID] || r.State == schema.SecretRuleOff {
			continue
		}
		if r.Regex == "" {
			return nil, errs.New(errs.ConfigInvalid, "secret rule "+r.RuleID+": a new rule needs a regex")
		}
		out = append(out, fromRow(r, ""))
	}
	return out, nil
}

// fromRow turns a row into a store rule (run on lore's regex engine).
func fromRow(r RuleRow, desc string) scan.Rule {
	return scan.Rule{
		ID: r.RuleID, Description: desc, Regex: r.Regex,
		Keywords: append([]string(nil), r.Keywords...), Entropy: r.Entropy, Source: scan.SourceStore,
	}
}

func validState(s string) bool {
	for _, st := range schema.SecretRuleStates() {
		if s == st {
			return true
		}
	}
	return false
}
