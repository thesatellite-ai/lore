package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Expansion ports for show --expand and render (SECRETS_AND_REFERENCES 12,
// 13.2): the same resolver the cache and lore_expand use (secrets and env
// never reach it), and the write scanner (gitleaks defaults + every attached
// store's secret_rules rows, read now) to scan the expanded output.

// RefResolver returns a resolver over the App's row files.
func (a *App) RefResolver(ctx context.Context) (refs.Resolver, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	return newResolver(st), nil
}

// OutputScanner returns a scanner built from the current rule rows, for
// scanning expanded output (13.2).
func (a *App) OutputScanner(ctx context.Context) (scan.Scanner, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	s, err := newScanner(ctx, st.byName, withRepoGitleaks(st.repoTop))
	if err != nil {
		return nil, err
	}
	s.bindEngine(st.eng)
	return s, nil
}

// HookScanner returns the scanner of the git hooks, export and lore check
// leaks: the team rules only (BATCH 8). Flow:
//
//	team    = every attached store but `me` / `me/<folder>`
//	scanner = newScanner(team) + D8 repo .gitleaks.toml      (team rules)
//	        + personal rows that ADD a rule id no team level or default has
//
// A personal row that turns a rule off or changes it never reaches the
// hooks, so a personal "off" never lets a finding into a shared commit; a
// personal added rule still applies (SECRETS_AND_REFERENCES 4: "applies to
// the person's captures and checks"; stricter is the safe side, and a
// migrated pii-patterns.txt keeps blocking).
func (a *App) HookScanner(ctx context.Context) (scan.Scanner, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	team := map[store.Source]store.Backend{}
	personal := map[store.Source]store.Backend{}
	for name, b := range st.byName {
		if direction.ClassOf(string(name), false) == direction.Personal {
			personal[name] = b
			continue
		}
		team[name] = b
	}
	s, err := newScanner(ctx, team, withRepoGitleaks(st.repoTop))
	if err != nil {
		return nil, err
	}
	s.bindEngine(st.eng)
	added, err := personalAdds(ctx, team, personal)
	if err != nil || added == nil {
		return s, err
	}
	return teamPlusAdds{team: s, adds: added}, nil
}

// personalAdds is a scanner of the personal rows that add a rule id no
// default and no team row defines (state on or changed, with a regex);
// nil when there are none.
func personalAdds(ctx context.Context, team, personal map[store.Source]store.Backend) (*gitleaks.Scanner, error) {
	if len(personal) == 0 {
		return nil, nil
	}
	mine, _, err := ruleRows(ctx, personal)
	if err != nil {
		return nil, err
	}
	teamRows, _, err := ruleRows(ctx, team)
	if err != nil {
		return nil, err
	}
	base, err := gitleaks.Defaults()
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, r := range base {
		known[r.ID] = true
	}
	for _, r := range teamRows {
		known[r.RuleID] = true
	}
	var adds []gitleaks.RuleRow
	for _, r := range mine {
		if known[r.RuleID] || r.State == schema.SecretRuleOff || r.Regex == "" {
			continue
		}
		adds = append(adds, r)
	}
	if len(adds) == 0 {
		return nil, nil
	}
	rules, err := gitleaks.Rules(nil, adds)
	if err != nil {
		return nil, err
	}
	return gitleaks.NewWith(rules, gitleaks.Options{})
}

// teamPlusAdds reports the findings of the team scanner and of the
// personal added rules.
type teamPlusAdds struct {
	team *liveScanner
	adds *gitleaks.Scanner
}

// Scan implements scan.Scanner.
func (s teamPlusAdds) Scan(field, text string) []scan.Finding {
	return append(s.team.Scan(field, text), s.adds.Scan(field, text)...)
}

// ScanReport implements scan.Reporter: degraded when either part is.
func (s teamPlusAdds) ScanReport(field, text string) scan.Report {
	r := s.team.ScanReport(field, text)
	p := s.adds.ScanReport(field, text)
	r.Findings = append(r.Findings, p.Findings...)
	if p.Degraded {
		r.Degraded = true
		if r.Note == "" {
			r.Note = p.Note
		}
	}
	return r
}
