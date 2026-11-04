package wire

import (
	"context"

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
