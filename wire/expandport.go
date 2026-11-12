package wire

import (
	"context"
	"github.com/thesatellite-ai/lore/model/errs"

	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/scan"
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

// OutputScanner returns the person's own scanner, built from the current
// rule rows, for scanning expanded output (13.2) and the person's own
// task notes: the team rules plus the personal rows (a personal off or
// change applies, BATCH 8: own captures).
func (a *App) OutputScanner(ctx context.Context) (scan.Scanner, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	s, err := newScanner(ctx, st.byName, withRepoGitleaks(st.repoTop), withFolders(st.folders), withOwnRules())
	if err != nil {
		return nil, err
	}
	s.bindEngine(st.eng)
	return s, nil
}

// HookScanner returns the scanner of the git hooks, export and lore check
// leaks: the team rules only (BATCH 8), the same rule set as the engine's
// write scanner. Flow:
//
//	rows    = every attached store's secret_rules rows
//	resolve = resolveRules(shared): personal off/changed dropped, personal
//	          ADDED rules kept, one row per rule_id (wire/scanner.go)
//	scanner = gitleaks defaults + resolved rows + D8 repo .gitleaks.toml
//
// A personal row that turns a rule off or changes it never reaches the
// hooks, so a personal "off" never lets a finding into a shared commit; a
// personal added rule still applies (SECRETS_AND_REFERENCES 4: "applies to
// the person's captures and checks"; stricter is the safe side, and a
// migrated pii-patterns.txt keeps blocking).
func (a *App) HookScanner(ctx context.Context) (scan.Scanner, error) {
	st, err := a.state(ctx)
	if err != nil {
		// Item 45: a repo lore is not set up in (no .lore, often a brand-new
		// repo with no commit yet) still gets the commit hook's secret check:
		// the built-in rules plus the repo's own .gitleaks.toml, with no lore
		// rows to add rules or allows from. Any other open failure is real.
		if errs.CodeOf(err) != errs.NotAStore {
			return nil, err
		}
		top := gitTop(ctx, a.opts.RepoDir)
		if top == "" {
			top = a.opts.RepoDir
		}
		return newScanner(ctx, nil, withRepoGitleaks(top))
	}
	s, err := newScanner(ctx, st.byName, withRepoGitleaks(st.repoTop), withFolders(st.folders))
	if err != nil {
		return nil, err
	}
	s.bindEngine(st.eng)
	return s, nil
}
