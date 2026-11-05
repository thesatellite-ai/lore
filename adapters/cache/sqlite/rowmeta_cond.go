package sqlite

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/metaquery"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Search conditions over the row_meta view (METADATA 3.17, OV39-CMP):
//
//	metaquery.Term ─► RowMetaCond(alias, t)
//	   Has  → alias.item_idx = -1
//	   Cond → value leaf, lower(text) equal (":" and "=" are one in search)
//	   Cmp  → ~=  lower(text) equal       != no leaf equals the value
//	          > >= < <=  value_num when the value is a number; the ISO
//	          text when it is a date (YYYY-MM-DD…); else E_META_OP
//	RowMetaComparable(alias, t) → the leaves a comparison can read (the
//	   rest of the key's rows are the "skipped" note of 3.17)

// isoDate matches a date or datetime value written ISO style.
var isoDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ][0-9:.+\-Z]*)?$`)

// isoDateGlob is the same test in SQL (the date prefix).
const isoDateGlob = `'[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]*'`

// RowMetaCond is the SQL condition on one row of the row_meta view named
// alias that t's term matches. Values are written as SQL literals (single
// quotes doubled), so the result can be put in a read-only lore sql query.
func RowMetaCond(alias string, t metaquery.Term) (string, error) {
	a := alias + "."
	switch t.Kind {
	case metaquery.Has:
		return a + "item_idx = -1", nil
	case metaquery.Cond:
		return a + "item_idx >= 0 AND " + a + "value_fold = lower(" + sqlLit(t.Value) + ")", nil
	case metaquery.Cmp:
		switch t.Op {
		case meta.OpLike:
			return a + "item_idx >= 0 AND " + a + "value_fold = lower(" + sqlLit(t.Value) + ")", nil
		case meta.OpNe:
			// a leaf that differs (the row's other leaves may still equal it;
			// "no leaf equals" is the caller's NOT EXISTS, see doc)
			return a + "item_idx >= 0 AND " + a + "value_fold <> lower(" + sqlLit(t.Value) + ")", nil
		case meta.OpGt, meta.OpGe, meta.OpLt, meta.OpLe:
			col, lit, err := cmpOperand(a, t)
			if err != nil {
				return "", err
			}
			return a + "item_idx >= 0 AND " + col + " IS NOT NULL AND " + col + " " + string(t.Op) + " " + lit, nil
		case meta.OpEq, meta.OpIn, meta.OpHas, meta.OpMissing:
			// not a Cmp operator: metaquery never makes these a Cmp term
		}
	case metaquery.Text:
		// free text is FTS5's, not a row_meta condition
	}
	return "", errs.New(errs.MetaOp, "meta: "+strconv.Quote(t.Word)+" is not a search condition")
}

// RowMetaComparable is the condition on a row_meta row whose leaf a
// comparison term t can read (a number for a number value, an ISO date
// for a date value); "" for a term that is not an ordering comparison.
// A key row matching neither is counted in the 3.17 "skipped" note.
func RowMetaComparable(alias string, t metaquery.Term) string {
	if t.Kind != metaquery.Cmp {
		return ""
	}
	switch t.Op {
	case meta.OpGt, meta.OpGe, meta.OpLt, meta.OpLe:
		col, _, err := cmpOperand(alias+".", t)
		if err != nil {
			return ""
		}
		return alias + ".item_idx >= 0 AND " + col + " IS NOT NULL"
	case meta.OpEq, meta.OpNe, meta.OpLike, meta.OpIn, meta.OpHas, meta.OpMissing:
		// equality-like: every leaf can be read, nothing is skipped
	}
	return ""
}

// cmpOperand picks the column and literal of an ordering comparison: a
// number compares value_num, an ISO date compares the date-shaped text
// (3.6: dates compare when both sides parse as YYYY-MM-DD).
func cmpOperand(a string, t metaquery.Term) (col, lit string, err error) {
	if f, perr := strconv.ParseFloat(t.Value, 64); perr == nil {
		return a + "value_num", strconv.FormatFloat(f, 'g', -1, 64), nil
	}
	if isoDate.MatchString(t.Value) {
		return "(CASE WHEN " + a + "value_text GLOB " + isoDateGlob + " THEN " + a + "value_text END)", sqlLit(t.Value), nil
	}
	return "", "", errs.New(errs.MetaOp, "meta: "+string(t.Op)+" compares numbers and dates; "+strconv.Quote(t.Value)+" is neither").
		WithHint("use key:value or key~=value for text")
}

// sqlLit is s as an SQL string literal.
func sqlLit(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
