package sqlite

// reflabel.go — ref values indexed by their target's label (OV34-REFLABEL,
// RES 3.12 / R8, FA-META-6): a resource whose indexable field holds a
// res_ id is also found by the target's name.
//
//	indexResource ─ resourceValueText (ids among the leaves)
//	   └ refLabels: name of each target (same source first) ─▶ field_values
//	upsertTyped(resources) ─ reindexReferrers: resources whose doc names
//	   the id get indexResource again (a rename, or a target applied after
//	   its referrer in one batch)

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// The fixed statements of this file (constant identifiers only).
const (
	// refLabelQuery takes (target id, source): the target's name, the
	// copy in the referrer's own source first.
	refLabelQuery = `SELECT "` + fieldName + `" FROM "` + schema.TableResources + `" WHERE id = ? AND "` + fieldName +
		`" IS NOT NULL ORDER BY ("` + colSource + `" = ?) DESC LIMIT 1`
	// referrersQuery takes (id, id): every live resource doc, other than
	// the target itself, whose text holds the id.
	referrersQuery = `SELECT source, doc FROM "` + tblRows + `" WHERE tbl = '` + schema.TableResources +
		`' AND deleted = 0 AND id != ? AND instr(doc, ?) > 0 ORDER BY source, id`
)

// resourceIDPrefix starts the text of a resource id ("res_").
const resourceIDPrefix = string(ids.PrefixResource) + "_"

// refIDs lists the resource ids among one value leaf: a string, or the
// strings of a list (raw canonical JSON such as ["res_a","res_b"], which
// has no spaces, so the words are split on the JSON punctuation too).
func refIDs(leaf string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(leaf, isRefSeparator) {
		if !strings.HasPrefix(w, resourceIDPrefix) {
			continue
		}
		if _, err := ids.Parse[ids.Resource](w); err == nil {
			out = append(out, w)
		}
	}
	return out
}

// isRefSeparator splits value text into words: space and JSON list
// punctuation (an id is prefix, "_" and hex, so none of these is in it).
func isRefSeparator(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(`[]",`, r)
}

// refLabels is the names of the resources text names, space separated
// ("" when none is known yet; reindexReferrers fills it in later).
func refLabels(ctx context.Context, tx *sql.Tx, source, text string) (string, error) {
	var names []string
	seen := map[string]bool{}
	for _, id := range refIDs(text) {
		if seen[id] {
			continue
		}
		seen[id] = true
		var name sql.NullString
		err := tx.QueryRowContext(ctx, refLabelQuery, id, source).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", dbErr("read ref label", err)
		}
		if name.String != "" {
			names = append(names, name.String)
		}
	}
	return strings.Join(names, " "), nil
}

// reindexReferrers re-indexes every resource whose row names id, so a
// renamed target (or one applied after its referrer) is found by its
// current name.
func reindexReferrers(ctx context.Context, tx *sql.Tx, p plan, id ids.AnyID) error {
	rs, err := tx.QueryContext(ctx, referrersQuery, id.String(), id.String())
	if err != nil {
		return dbErr("find ref referrers", err)
	}
	type held struct {
		source string
		r      row.Row
	}
	var refs []held
	for rs.Next() {
		var source string
		var doc []byte
		if serr := rs.Scan(&source, &doc); serr != nil {
			return errors.Join(dbErr("find ref referrers", serr), rs.Close())
		}
		r, derr := row.Decode(doc)
		if derr != nil {
			continue // a newer _v: kept in _rows only (see project)
		}
		refs = append(refs, held{source, r})
	}
	if cerr := errors.Join(rs.Err(), rs.Close()); cerr != nil {
		return dbErr("find ref referrers", cerr)
	}
	for _, h := range refs {
		info := resFTSInfo{}
		if kind := kindOf(h.r); !kind.IsZero() {
			if info, err = ftsInfoFromTables(ctx, tx, kind); err != nil {
				return err
			}
		}
		if ierr := indexResource(ctx, tx, p, h.source, h.r, info); ierr != nil {
			return ierr
		}
	}
	return nil
}
