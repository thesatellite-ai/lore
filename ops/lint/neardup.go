package lint

// neardup.go — near_duplicate: two live rows that say the same thing
// (devco D-926). An import and its rewrite a month later both stayed live,
// so every pinned rule rendered twice and search returned both, and lint
// only looked at pinned rows under --context, with a shingle overlap a
// reworded rewrite falls below.
//
//	nearDuplicates(rows, pinnedOnly)
//	  ├─ rows sorted oldest first (created_at, then id)
//	  ├─ logic/neardup.Within: source_ref names a live row (rewrite of),
//	  │                        same title/summary/first line, or bodies
//	  │                        sharing 80% of their words
//	  ├─ pinnedOnly (--context): also three-word shingle overlap over 0.5
//	  └─ one finding per pair on the OLDER row, fix = archive it
//	     (lore <kind> archive --id <older>), the newer row named in the message
//
// Reported, never merged: which wording to keep is a person's call.

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strconv"

	"github.com/thesatellite-ai/lore/logic/neardup"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// Fields the near-duplicate check reads besides fBody.
const (
	fTitle     = "title"
	fSourceRef = "source_ref"
)

// archiveNouns maps each table the default near_duplicate check scans to
// the command noun whose `archive --id` verb hides a row of it; a table
// without that verb is not scanned (the fix printed must run).
var archiveNouns = map[row.Table]string{
	schema.TableRules:     "rule",
	schema.TableHotfixes:  "hotfix",
	schema.TableDecisions: "decision",
	schema.TableMemories:  "memory",
	schema.TablePatterns:  "pattern",
	schema.TablePlaybooks: "playbook",
	schema.TablePrompts:   "prompt",
}

// ArchiveNouns lists the command nouns near_duplicate prints in its fix
// (`lore <noun> archive --id`), sorted; a test checks each verb exists.
func ArchiveNouns() []string {
	out := make([]string, 0, len(archiveNouns))
	for _, n := range archiveNouns {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// FindNearDuplicates is the near_duplicate findings of every attached
// store (lore doctor counts them and points at lore lint --code
// near_duplicate, devco D-926); it reads only the tables it scans.
func FindNearDuplicates(ctx context.Context, env Env) ([]Finding, error) {
	all, err := ops{env: env}.rowsOf(ctx, slices.Sorted(maps.Keys(archiveNouns)))
	if err != nil {
		return nil, err
	}
	return nearDuplicates(nearDupRows(all), false), nil
}

// nearDupRows is every live, unarchived row of the scanned tables.
func nearDupRows(all map[row.Table][]sourced) []sourced {
	var out []sourced
	for t := range archiveNouns {
		for _, s := range all[t] {
			if text(s.r, fArchivedAt) == "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// nearDuplicates reports each pair of rows that repeat each other, once,
// on the older row; pinnedOnly adds the --context shingle overlap.
func nearDuplicates(rows []sourced, pinnedOnly bool) []Finding {
	rows = slices.Clone(rows)
	slices.SortFunc(rows, func(a, b sourced) int {
		return cmp.Or(a.r.CreatedAt.Compare(b.r.CreatedAt), cmp.Compare(a.r.ID.String(), b.r.ID.String()))
	})
	items := make([]neardup.Item, len(rows))
	at := make(map[string]int, len(rows))
	for i, s := range rows {
		body := text(s.r, fBody)
		items[i] = neardup.Item{Table: s.r.Table.String(), ID: s.r.ID.String(), Body: body, SourceRef: text(s.r, fSourceRef),
			Title: neardup.Headline(text(s.r, fTitle), text(s.r, schema.FieldSummary), body)}
		at[items[i].ID] = i
	}
	type pair struct{ old, new int }
	var pairs []pair
	why := map[pair]string{}
	for _, m := range neardup.Within(items) {
		p := pair{at[m.Existing.ID], at[m.New.ID]}
		pairs = append(pairs, p)
		why[p] = string(m.Why)
		if m.Why == neardup.SimilarBody {
			why[p] += " " + strconv.Itoa(m.Percent) + "%"
		}
	}
	if pinnedOnly {
		sh := make([]map[string]bool, len(rows))
		for i, s := range rows {
			sh[i] = shingles(text(s.r, fBody))
		}
		for i := range rows {
			for j := i + 1; j < len(rows); j++ {
				p := pair{i, j}
				if _, ok := why[p]; !ok && jaccard(sh[i], sh[j]) > nearDupOverlap {
					pairs = append(pairs, p)
					why[p] = "most of the wording is shared"
				}
			}
		}
	}
	slices.SortStableFunc(pairs, func(a, b pair) int { return cmp.Or(cmp.Compare(a.old, b.old), cmp.Compare(a.new, b.new)) })
	out := make([]Finding, 0, len(pairs))
	for _, p := range pairs {
		o, n := rows[p.old], rows[p.new]
		oid, nid := o.r.ID.String(), n.r.ID.String()
		noun := archiveNouns[o.r.Table]
		out = append(out, Finding{Code: NearDuplicate, ID: oid, Store: string(o.src),
			Message: "near duplicate of the newer " + nid + " (" + string(n.src) + "; " + why[p] + "), both live, so both are rendered and found; compare them with lore show --id " + oid + " and lore show --id " + nid,
			Fix:     "keep the newer: lore " + noun + " archive --id " + oid + " (or archive " + nid + " instead: lore " + archiveNouns[n.r.Table] + " archive --id " + nid + ")"})
	}
	return out
}
