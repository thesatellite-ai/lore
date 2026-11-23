package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/factstale"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
)

// FactChecker implements ops/lint.FactEnv and ops/ask.FactEnv (devco:D-941):
// a checker that asks git about the code repository this command runs in,
// where fact_evidence paths are relative to. Nil outside a git repository
// (a fact is then never reported stale on a guess).
func (a *App) FactChecker(ctx context.Context) *factstale.Checker {
	top, err := a.knownRepoTop(ctx)
	if err != nil || top == "" {
		return nil
	}
	return factstale.NewChecker(func(ctx context.Context, args ...string) ([]byte, error) {
		return gitRun{dir: top, args: args}.output(ctx)
	})
}

// markStaleHits sets Stale on every hit whose row states a fact about code
// that changed after it was verified (devco:D-941): search prints the mark
// and ranks the hit below a fresh one with the same match.
func (a *App) markStaleHits(ctx context.Context, hits []opssearch.Hit) {
	var chk *factstale.Checker
	for i := range hits {
		if hits[i].Fact == nil || hits[i].InTrash {
			continue
		}
		if chk == nil {
			if chk = a.FactChecker(ctx); chk == nil {
				return
			}
		}
		hits[i].Stale = chk.Check(ctx, *hits[i].Fact).Note()
	}
}
