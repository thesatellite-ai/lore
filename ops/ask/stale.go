package ask

// stale.go — a row stating a fact about code whose evidence changed in git
// after it was verified (devco:D-941, bug hunt H-190): the answer marks it
// ("stale: evidence changed since <day> (<files>)") and ranks it below a
// fresh row with the same score, so an agent checks it before relying on
// it.
//
//	lens: factSQL ─► facts (row id → fact_evidence items, fact_verified_at)
//	after the lens: Env.FactChecker (optional, nil outside a git repo)
//	  └─ textRow.Stale for each row that names evidence ─► topRows order, Row.Stale

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/factstale"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/store"
)

// FactEnv is the optional part of Env that answers git questions about
// the code repository the question is asked in (*wire.App implements it);
// FactChecker is nil outside a git repository.
type FactEnv interface {
	FactChecker(ctx context.Context) *factstale.Checker
}

// factSQL reads the fact keys of every row, one line per item: clock-free
// and input-free, so the read server keeps its answer (FileOnly).
const factSQL = "SELECT entity_id, key_name, value_text FROM row_meta_all WHERE key_name IN ('" + meta.KeyFactEvidence + "', '" + meta.KeyFactVerifiedAt + "') AND item_idx >= 0 AND path = ''"

// factItems is one row's fact keys as the cache holds them.
type factItems struct {
	evidence []string
	verified string
}

// loadFacts reads every row's fact keys (row id → items).
func loadFacts(ctx context.Context, run Query) (map[string]factItems, error) {
	res, err := run(ctx, factSQL)
	if err != nil {
		return nil, err
	}
	out := map[string]factItems{}
	for _, r := range res.Rows {
		if len(r) < 3 || r[2] == nil {
			continue
		}
		id, f := cell(r[0]), out[cell(r[0])]
		switch cell(r[1]) {
		case meta.KeyFactEvidence:
			f.evidence = append(f.evidence, cell(r[2]))
		case meta.KeyFactVerifiedAt:
			f.verified = cell(r[2])
		}
		out[id] = f
	}
	return out, nil
}

// storeFlag is the --store a write of a row printed at place needs: none
// for the repo store.
func storeFlag(place string) string {
	if place == string(store.SourceRepo) {
		return ""
	}
	return place
}

// markStale sets Stale (and Reverify, devco:D-953) on each row whose evidence changed after it was
// verified; nothing when env checks no facts.
func markStale(ctx context.Context, env Env, rows []textRow, facts map[string]factItems) {
	fe, ok := env.(FactEnv)
	if !ok || len(facts) == 0 {
		return
	}
	var chk *factstale.Checker
	now := opclock.Now(env)
	for i := range rows {
		f, ok := facts[rows[i].ID]
		if !ok {
			continue
		}
		cl, ok := factstale.ClaimOfItems(f.evidence, f.verified)
		if !ok || !cl.Checkable() {
			continue
		}
		if chk == nil {
			if chk = fe.FactChecker(ctx); chk == nil {
				return
			}
		}
		v := chk.Check(ctx, cl)
		rows[i].Stale, rows[i].Reverify = v.Note(), factstale.Reverify(rows[i].ID, storeFlag(rows[i].Place), v, now)
	}
}
