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
	"github.com/thesatellite-ai/lore/model/value"
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
// near_duplicate, devco D-926); it reads only the tables it scans, one
// store's table at a time, and keeps each row as its id, version and
// word numbers, never its text (devco:D-1016: a 1M-commit store held
// every scanned row decoded for the whole check).
func FindNearDuplicates(ctx context.Context, env Env) ([]Finding, error) {
	v := neardup.NewVocab()
	var rows []ndRow
	err := ops{env: env}.eachRow(ctx, slices.Sorted(maps.Keys(archiveNouns)), func(_ row.Table, s sourced) {
		if text(s.r, fArchivedAt) == "" {
			rows = append(rows, newNDRow(v, s))
		}
	})
	if err != nil {
		return nil, err
	}
	return nearDupFindings(oneNDCopyEach(rows), nil, true), nil
}

// ndRow is one row as near_duplicate keeps it: the row without its
// fields (id, table, store, created_at and version: what ordering, copy
// detection and the finding read) and its neardup doc.
type ndRow struct {
	sourced
	doc neardup.Doc
}

// newNDRow prepares s for near_duplicate with v and lets its text go.
func newNDRow(v *neardup.Vocab, s sourced) ndRow {
	body := text(s.r, fBody)
	doc := v.Doc(neardup.Item{Table: s.r.Table.String(), ID: s.r.ID.String(), Body: body, SourceRef: text(s.r, fSourceRef),
		Title: neardup.Headline(text(s.r, fTitle), text(s.r, schema.FieldSummary), body)})
	s.r.Fields, s.r.Meta, s.r.Reserved, s.r.Origin = nil, nil, nil, value.JSON{}
	return ndRow{sourced: s, doc: doc}
}

// oneNDCopyEach is oneCopyEach over prepared rows.
func oneNDCopyEach(rows []ndRow) []ndRow {
	plain := make([]sourced, len(rows))
	for i, r := range rows {
		plain[i] = r.sourced
	}
	stale := staleCopies(plain) // devco:D-929: two copies of one row are copied_id, not a near duplicate
	return slices.DeleteFunc(rows, func(r ndRow) bool { return stale[r.src][r.r.ID.String()] })
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
	return oneCopyEach(out) // devco:D-929: two copies of one row are copied_id, not a near duplicate
}

// nearDuplicates reports each pair of rows that repeat each other, once,
// on the older row; pinnedOnly adds the --context shingle overlap.
func nearDuplicates(rows []sourced, pinnedOnly bool) []Finding {
	v := neardup.NewVocab()
	prepared := make([]ndRow, len(rows))
	var sh map[string]map[string]bool
	if pinnedOnly {
		sh = make(map[string]map[string]bool, len(rows))
	}
	for i, s := range rows {
		if pinnedOnly {
			sh[s.r.ID.String()] = shingles(text(s.r, fBody))
		}
		prepared[i] = newNDRow(v, s)
	}
	return nearDupFindings(prepared, sh, !pinnedOnly) // --context (pinned rows) pairs exactly, as render does
}

// nearDupFindings is nearDuplicates over prepared rows; shingles (the
// --context pinned rows', by id) adds the three-word overlap pairs; store
// pairs a whole store's rows with neardup.WithinStore (devco:D-1016).
func nearDupFindings(rows []ndRow, shingles map[string]map[string]bool, store bool) []Finding {
	rows = slices.Clone(rows)
	slices.SortFunc(rows, func(a, b ndRow) int {
		return cmp.Or(a.r.CreatedAt.Compare(b.r.CreatedAt), cmp.Compare(a.r.ID.String(), b.r.ID.String()))
	})
	docs := make([]neardup.Doc, len(rows))
	at := make(map[string]int, len(rows))
	for i, r := range rows {
		docs[i] = r.doc
		at[r.doc.ID] = i
	}
	type pair struct{ old, new int }
	var pairs []pair
	why := map[pair]string{}
	join := neardup.WithinDocs
	if store {
		join = neardup.WithinStore
	}
	for _, m := range join(docs) {
		p := pair{at[m.Existing.ID], at[m.New.ID]}
		pairs = append(pairs, p)
		why[p] = string(m.Why)
		if m.Why == neardup.SimilarBody {
			why[p] += " " + strconv.Itoa(m.Percent) + "%"
		}
	}
	if shingles != nil {
		for i := range rows {
			for j := i + 1; j < len(rows); j++ {
				p := pair{i, j}
				if _, ok := why[p]; !ok && jaccard(shingles[rows[i].doc.ID], shingles[rows[j].doc.ID]) > nearDupOverlap {
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
