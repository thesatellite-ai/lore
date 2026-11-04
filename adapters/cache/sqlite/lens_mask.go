package sqlite

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// MaskedValue is what a masked lens shows in place of a sensitive value
// (RQ1), the same text `lore resource show` prints.
const MaskedValue = "•••"

// fieldSensitive is the kind_fields column that marks a field sensitive
// (stored 0 or 1, spec 4.4 bool affinity).
const fieldSensitive = "sensitive"

// dbPageTable is SQLite's raw page reader. modernc.org/sqlite is compiled
// with SQLITE_ENABLE_DBPAGE_VTAB, and sqlite_dbpage(<schema>) returns the
// bytes of every page of an attached file, sensitive values included, with
// the schema name computable at run time (char(115,95,48)) so no literal
// check can see it. A masked lens therefore refuses the name itself.
const dbPageTable = "sqlite_dbpage"

// mask is what a masked lens hides: per table (a kind's res_<name> table
// and its stable res_<knd id> view), the sensitive columns; and every
// sensitive fld_ id, whose entry in resources.fields is masked too. The
// zero mask hides nothing (an unmasked lens).
//
// Why union over sources: a kind's sensitivity is decided by its
// kind_fields rows, and two stores could disagree on a copy; hiding a
// column any source calls sensitive is the safe answer.
type mask struct {
	cols     map[string][]string
	fieldIDs []string
	// on is a masked lens; metaKeyIDs are the meta keys any source's
	// meta_keys marks sensitive. row_meta value columns are masked for
	// those keys and for every entry that says sensitive itself (3.20).
	on         bool
	metaKeyIDs []string
}

// sensitive reads every source's sensitive kind fields. A source that has
// kinds and kind_fields but cannot be read fails the open (fail closed:
// never show a lens whose masking is unknown).
func (l *Lens) sensitive(ctx context.Context, tables map[string][]srcTable) (mask, error) {
	m := mask{cols: map[string][]string{}, on: true}
	has := func(table string, src int) bool {
		return slices.ContainsFunc(tables[table], func(st srcTable) bool { return st.src == src })
	}
	for i := range l.sources {
		if !has(schema.TableKinds, i) || !has(schema.TableKindFields, i) {
			continue
		}
		rs, err := l.conn.QueryContext(ctx, l.sql[i].sensitive)
		if err != nil {
			return mask{}, dbErr("read sensitive fields of "+quoteText(l.sources[i].Name), err)
		}
		for rs.Next() {
			var kindID, kindName, fieldID, field string
			if scanErr := rs.Scan(&kindID, &kindName, &fieldID, &field); scanErr != nil {
				_ = rs.Close()
				return mask{}, dbErr("read sensitive fields", scanErr)
			}
			for _, t := range []string{schema.KindTablePrefix + kindName, schema.KindTablePrefix + kindID} {
				if !slices.Contains(m.cols[t], field) {
					m.cols[t] = append(m.cols[t], field)
				}
			}
			if !slices.Contains(m.fieldIDs, fieldID) {
				m.fieldIDs = append(m.fieldIDs, fieldID)
			}
		}
		err = rs.Err()
		_ = rs.Close()
		if err != nil {
			return mask{}, dbErr("read sensitive fields", err)
		}
	}
	slices.Sort(m.fieldIDs)
	for i := range l.sources {
		if !has(schema.TableMetaKeys, i) {
			continue
		}
		keys, err := l.sensitiveMetaKeys(ctx, i)
		if err != nil {
			return mask{}, err
		}
		for _, k := range keys {
			if !slices.Contains(m.metaKeyIDs, k) {
				m.metaKeyIDs = append(m.metaKeyIDs, k)
			}
		}
	}
	slices.Sort(m.metaKeyIDs)
	return m, nil
}

// sensitiveMetaKeys reads source i's sensitive meta key ids (fail closed:
// an unreadable registry fails the open).
func (l *Lens) sensitiveMetaKeys(ctx context.Context, i int) ([]string, error) {
	rs, err := l.conn.QueryContext(ctx, l.sql[i].metaSensitive)
	if err != nil {
		return nil, dbErr("read sensitive meta keys of "+quoteText(l.sources[i].Name), err)
	}
	var out []string
	for rs.Next() {
		var id string
		if scanErr := rs.Scan(&id); scanErr != nil {
			_ = rs.Close()
			return nil, dbErr("read sensitive meta keys", scanErr)
		}
		out = append(out, id)
	}
	err = rs.Err()
	_ = rs.Close()
	if err != nil {
		return nil, dbErr("read sensitive meta keys", err)
	}
	return out, nil
}

// metaExpr masks one row_meta value column (cq) of a sensitive entry.
func (m mask) metaExpr(cq string) string {
	cond := `"sensitive" = 1`
	if len(m.metaKeyIDs) > 0 {
		idList := make([]string, len(m.metaKeyIDs))
		for i, id := range m.metaKeyIDs {
			idList[i] = literal(id)
		}
		cond += ` OR "key_id" IN (` + strings.Join(idList, ", ") + `)`
	}
	return "CASE WHEN " + cq + " IS NULL THEN NULL WHEN " + cond + " THEN " + literal(MaskedValue) + " ELSE " + cq + " END"
}

// columns is the select list of one source's copy of table: every column
// of all in order, NULL where the source lacks it, a sensitive column as
// MaskedValue (NULL stays NULL, so "is it set?" still answers), and
// resources.fields with each sensitive entry's value replaced.
func (m mask) columns(table string, all, have []string) (string, error) {
	sel := make([]string, 0, len(all))
	for _, c := range all {
		cq, err := ident(c)
		if err != nil {
			return "", err
		}
		switch {
		case !slices.Contains(have, c):
			sel = append(sel, "NULL AS "+cq)
		case m.on && table == ViewRowMeta && slices.Contains(rowMetaValueCols, c):
			sel = append(sel, m.metaExpr(cq)+" AS "+cq)
		case slices.Contains(m.cols[table], c):
			sel = append(sel, "CASE WHEN "+cq+" IS NULL THEN NULL ELSE "+literal(MaskedValue)+" END AS "+cq)
		case table == schema.TableResources && c == fieldResVals && len(m.fieldIDs) > 0:
			sel = append(sel, m.fieldsExpr(cq)+" AS "+cq)
		default:
			sel = append(sel, cq)
		}
	}
	return strings.Join(sel, ", "), nil
}

// fieldsExpr masks the sensitive entries of a resources.fields JSON column.
// The stored shape is {fld_id: {name, v}} (spec 4.4): a sensitive entry
// keeps its name and gets v = MaskedValue. Any other shape of a sensitive
// entry (a flat value an older or broken writer left) is masked whole, and
// a column that is not a JSON object is masked whole (fail closed: the
// mask must not depend on every writer having been right).
//
// The object is rebuilt with json_each/json_group_object so each entry is
// decided on its own; every non-sensitive entry is passed through with its
// JSON type (objects and arrays via json(), true/false/null re-spelled, as
// json_each hands those back as SQL values).
func (m mask) fieldsExpr(cq string) string {
	idList := make([]string, len(m.fieldIDs))
	for i, id := range m.fieldIDs {
		idList[i] = literal(id)
	}
	masked := literal(MaskedValue)
	entry := "CASE WHEN key IN (" + strings.Join(idList, ", ") + ") THEN " +
		"CASE WHEN type = 'object' THEN json_set(value, " + literal("$."+resValueKey) + ", " + masked + ") ELSE " + masked + " END " +
		"WHEN type IN ('object', 'array') THEN json(value) " +
		"WHEN type = 'true' THEN json('true') WHEN type = 'false' THEN json('false') WHEN type = 'null' THEN json('null') " +
		"ELSE value END"
	return "CASE WHEN " + cq + " IS NULL THEN NULL " +
		"WHEN json_valid(" + cq + ") AND json_type(" + cq + ") = 'object' THEN " +
		"(SELECT json_group_object(key, " + entry + ") FROM json_each(" + cq + ")) " +
		"ELSE " + masked + " END"
}

// hiddenNames are the names a masked lens refuses in a query: the schema
// name of every attached file (its tables are the unmasked copy) and the
// raw page reader.
func (l *Lens) hiddenNames() []string {
	out := []string{dbPageTable}
	for i := range l.sources {
		out = append(out, attachedName(i))
	}
	return out
}

// checkHidden refuses a query that names any of hidden as an identifier or
// a string literal (case-insensitive, as SQLite compares names), outside
// comments. A string literal counts because SQLite accepts a quoted name in
// several places ('s_0'.memories, pragma arguments) and a double-quoted
// unknown identifier falls back to a string.
func checkHidden(q string, hidden []string) error {
	for _, tok := range nameTokens(q) {
		for _, h := range hidden {
			if strings.EqualFold(tok, h) {
				return errs.New(errs.InvalidInput, "sqlite lens: "+strconv.Quote(tok)+
					" reads past the masked views; query the <table>_all views (or pass --show-sensitive)")
			}
		}
	}
	return nil
}

// nameTokens lists every bare word, quoted identifier ("x", `x`, [x]) and
// string literal ('x') of q, unquoted, skipping comments.
func nameTokens(q string) []string {
	lx := lexer{s: q}
	var out []string
	for {
		lx.skipSpace()
		if lx.i >= len(lx.s) {
			return out
		}
		switch c := lx.s[lx.i]; {
		case c == '\'' || c == '"' || c == '`':
			out = append(out, lx.unquote(c, c))
		case c == '[':
			out = append(out, lx.unquote('[', ']'))
		case isWordByte(c):
			start := lx.i
			for lx.i < len(lx.s) && isWordByte(lx.s[lx.i]) {
				lx.i++
			}
			out = append(out, lx.s[start:lx.i])
		default:
			lx.i++
		}
	}
}

// isWordByte is a byte of a bare SQLite word: letters, digits, '_', '$'
// and any byte of a multi-byte UTF-8 character (SQLite allows those in
// identifiers).
func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 || (c >= '0' && c <= '9') || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

// unquote reads the quoted run opened at l.i and returns its content with
// a doubled closer collapsed (']' has no escape).
func (l *lexer) unquote(opener, closer byte) string {
	start := l.i
	l.quoted(closer)
	end := l.i
	if end > start+1 && l.s[end-1] == closer {
		end--
	}
	body := l.s[start+1 : end]
	if opener == closer {
		body = strings.ReplaceAll(body, string([]byte{closer, closer}), string(closer))
	}
	return body
}
