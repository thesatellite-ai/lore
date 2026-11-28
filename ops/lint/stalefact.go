package lint

// stalefact.go — stale_fact: a row states a fact about code, names its
// evidence (fact_evidence: <path>:<line>, <path> or a commit sha) and the
// day it was checked (fact_verified_at), and an evidence file changed in
// git after that day (devco:D-941, bug hunt H-190). The row may no longer
// be true; nothing told the reader so.
//
//	staleFacts(all, checker, today)
//	  ├─ every live row whose metadata names fact_evidence
//	  ├─ factstale.Checker.Prime + Check: one git log for every row's evidence (H-209)
//	  └─ one finding per stale row: the changed files with their commit,
//	     fix = after re-checking, lore meta set --id <id> --pair fact_verified_at=<day>
//	     (today, or the newest change's day when a commit is dated later)
//
// Reported, never changed on its own: only a person or agent that read
// the code again can say the fact still holds.

import (
	"context"
	"slices"
	"strconv"
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

// UncheckedFact is the finding of fact evidence the stale check could not
// read (bug hunt H-298, devco:D-1033): one per row naming a file this repo
// does not hold while another of its files is here, and one counting the
// rows about code this repo does not hold (or every such row, outside a
// git repo). Ask and search say nothing of the latter; lint and doctor
// count them, so a store checked from the wrong folder is never quietly
// fresh.
const UncheckedFact Code = "unchecked_fact"

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
	// devco:D-1016: only the rows stating a checkable fact are kept, one
	// store's table read at a time, not every row of every table
	all := map[row.Table][]sourced{}
	err := ops{env: env}.eachRow(ctx, builtinTables(), func(t row.Table, s sourced) {
		if cl, ok := factstale.ClaimOf(s.r); ok && cl.Checkable() {
			all[t] = append(all[t], s)
		}
	})
	if err != nil {
		return nil, err
	}
	if chk == nil {
		return noRepoFacts(all), nil
	}
	return staleFacts(ctx, all, chk, opclock.Now(env)), nil
}

// noRepoFacts is the one unchecked_fact finding counting the rows with
// checkable evidence when the command runs outside a git repo.
func noRepoFacts(all map[row.Table][]sourced) []Finding {
	n := 0
	seen := map[string]bool{}
	for _, rows := range all {
		for _, s := range rows {
			if cl, ok := factstale.ClaimOf(s.r); ok && cl.Checkable() && !seen[s.r.ID.String()] {
				seen[s.r.ID.String()] = true
				n++
			}
		}
	}
	return uncheckedCount(n, "there is no git repo here")
}

// uncheckedCount is the unchecked_fact finding counting n rows whose
// evidence could not be checked (none when n is 0).
func uncheckedCount(n int, why string) []Finding {
	if n == 0 {
		return nil
	}
	return []Finding{{Code: UncheckedFact, Rows: n,
		Message: strconv.Itoa(n) + " row(s) name fact_evidence that could not be checked: " + why + "; run lore lint --code " + string(StaleFact) + " inside the code repo they are about"}}
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
		return noRepoFacts(all)
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
	// bug hunt H-209: one git log for every row's evidence, not one per item
	var claims []factstale.Claim
	for _, t := range tables {
		for _, s := range all[t] {
			if cl, ok := factstale.ClaimOf(s.r); ok && cl.Checkable() {
				claims = append(claims, cl)
			}
		}
	}
	chk.Prime(ctx, claims)
	var out []Finding
	seen := map[string]bool{}
	unreachable := 0
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
			if v.Unreachable {
				seen[id] = true
				unreachable++
			}
			if len(v.Unfound) > 0 && !seen[id] {
				where := ""
				if s.src != store.SourceRepo {
					where = " --store " + string(s.src)
				}
				out = append(out, Finding{Code: UncheckedFact, ID: id, Store: string(s.src),
					Message: id + " names fact_evidence this repo does not hold (" + strings.Join(v.Unfound, ", ") + "), so it could not be checked; correct the path with lore meta set --id " + id + where + " --pair " + meta.KeyFactEvidence + "=<path>"})
			}
			if !v.Stale {
				continue
			}
			seen[id] = true
			var files []string
			for _, c := range v.Changed {
				files = append(files, c.Path+" (in "+c.Commit+" on "+c.At.Format(time.DateOnly)+")")
			}
			where, st := "", ""
			if s.src != store.SourceRepo {
				st = string(s.src)
				where = " --store " + st
			}
			out = append(out, Finding{Code: StaleFact, ID: id, Store: string(s.src),
				Message: id + " states a fact about code whose evidence changed after " + v.Since + ": " + strings.Join(files, ", ") + "; read the row (lore show --id " + id + where + ") and the code again",
				Fix:     factstale.Reverify(id, st, v, now)}) // the fix must clear the finding (devco:D-953: one wording)
		}
	}
	return append(out, uncheckedCount(unreachable, "none of their files are in this repo")...)
}
