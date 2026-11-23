package lint

// stalefact.go — stale_fact: a row states a fact about code, names its
// evidence (fact_evidence: <path>:<line>, <path> or a commit sha) and the
// day it was checked (fact_verified_at), and an evidence file changed in
// git after that day (devco:D-941, bug hunt H-190). The row may no longer
// be true; nothing told the reader so.
//
//	staleFacts(all, checker, today)
//	  ├─ every live row whose metadata names fact_evidence
//	  ├─ factstale.Checker.Check: one git log per evidence item
//	  └─ one finding per stale row: the changed files with their commit,
//	     fix = after re-checking, lore meta set --id <id> --pair fact_verified_at=<day>
//	     (today, or the newest change's day when a commit is dated later)
//
// Reported, never changed on its own: only a person or agent that read
// the code again can say the fact still holds.

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/factstale"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/store"
)

// StaleFact is the finding of a fact whose evidence changed after it was
// verified.
const StaleFact Code = "stale_fact"

// FactEnv is the optional part of Env that answers git questions about
// the code repository the command runs in (*wire.App implements it).
// FactChecker is nil outside a git repository: no fact is then checked.
type FactEnv interface {
	FactChecker(ctx context.Context) *factstale.Checker
}

// FindStaleFacts is the stale_fact findings of every attached store (lore
// doctor counts them and points at lore lint --code stale_fact).
func FindStaleFacts(ctx context.Context, env Env) ([]Finding, error) {
	fe, ok := env.(FactEnv)
	if !ok {
		return nil, nil
	}
	chk := fe.FactChecker(ctx)
	if chk == nil {
		return nil, nil
	}
	all, err := ops{env: env}.rows(ctx)
	if err != nil {
		return nil, err
	}
	return staleFacts(ctx, all, chk, opclock.Now(env)), nil
}

// staleFactsOf is the stale_fact findings of rows already read (nil when
// env checks no facts).
func (o ops) staleFactsOf(ctx context.Context, all map[row.Table][]sourced) []Finding {
	fe, ok := o.env.(FactEnv)
	if !ok {
		return nil
	}
	chk := fe.FactChecker(ctx)
	if chk == nil {
		return nil
	}
	return staleFacts(ctx, all, chk, opclock.Now(o.env))
}

// staleFacts reports every live row whose fact evidence changed after it
// was verified, tables in name order, rows in list order.
func staleFacts(ctx context.Context, all map[row.Table][]sourced, chk *factstale.Checker, now time.Time) []Finding {
	tables := make([]row.Table, 0, len(all))
	for t := range all {
		tables = append(tables, t)
	}
	slices.Sort(tables)
	today := now.UTC().Format(time.DateOnly)
	var out []Finding
	seen := map[string]bool{}
	for _, t := range tables {
		for _, s := range all[t] {
			cl, ok := factstale.ClaimOf(s.r)
			if !ok || !cl.Checkable() {
				continue
			}
			id := s.r.ID.String()
			if seen[id] {
				continue // two copies of one row: copied_id reports that
			}
			v := chk.Check(ctx, cl)
			if !v.Stale {
				continue
			}
			seen[id] = true
			var files []string
			day := today
			for _, c := range v.Changed {
				files = append(files, c.Path+" (in "+c.Commit+" on "+c.At.Format(time.DateOnly)+")")
				// the fix must clear the finding: a commit dated after
				// today (a skewed clock, a rebase) needs its own day
				day = max(day, c.At.UTC().Format(time.DateOnly))
			}
			where := ""
			if s.src != store.SourceRepo {
				where = " --store " + string(s.src)
			}
			out = append(out, Finding{Code: StaleFact, ID: id, Store: string(s.src),
				Message: id + " states a fact about code whose evidence changed after " + v.Since + ": " + strings.Join(files, ", ") + "; read the row (lore show --id " + id + where + ") and the code again",
				Fix:     "when it still holds: lore meta set --id " + id + where + " --pair " + meta.KeyFactVerifiedAt + "=" + day + "; when it does not: edit the row and its " + meta.KeyFactEvidence})
		}
	}
	return out
}
