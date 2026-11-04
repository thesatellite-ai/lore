package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Row metadata in the cache (METADATA 4, 3.16, 3.17):
//
//	_rows.doc."meta" ─► VIEW row_meta (json_each + json_tree, one row per value)
//	                 └► lens: row_meta_all / masked TEMP VIEW row_meta (sensitive → •••)
//	project ─► metaText(row) ─► FTS column f_meta (non-sensitive values only)
//	meta_keys row applied ─► reindexMetaKey: rows using the key re-projected
//
// OV39-VIEW: row_meta is a view over `_rows`, not a projected table: it can
// never be stale against the row files (a delete, a trash or a registry
// change shows at once) and needs no re-projection on registry change
// (design 4 asks for a table with surrogate keys; that is a measured
// optimisation for later, D26).
const (
	// ViewRowMeta is the readable metadata view (one row per value).
	ViewRowMeta = "row_meta"
	// ftsMeta is the FTS pseudo-field holding a row's metadata values
	// (3.17 step 1). "meta" is an envelope key, so no field can take it.
	ftsMeta = row.KeyMeta
	// ftsWeightMeta is the bm25 weight of the meta column (3.17: weight 2).
	ftsWeightMeta = 2
)

// rowMetaValueCols are the row_meta columns a masked lens hides for a
// sensitive key (3.20: entity, key and scope stay readable).
var rowMetaValueCols = []string{"value_text", "value_fold", "value_num", "value_bool", "value_id", "value_json"}

// rowMetaDDL creates the view (idempotent); appended to internalDDL.
//
// Columns: _source, entity_table, entity_id, key_id, key_name (name, plus
// "." + path for an object field), scope, sensitive (the entry's flag),
// path (” top level, 'a.b' for object fields), item_idx (-1 the presence
// row carrying the whole value in value_json, 0 a single value, 0..n-1
// list elements), value_text (canonical text; an allowed value's name),
// value_fold (lower(value_text), for ~=), value_num, value_bool, value_id
// (the val_ id of an allowed value), value_json. Tombstones and rows in the
// trash (deleted_at set, DELETION 3.2) have no metadata rows (3.22).
var rowMetaDDL = []string{
	`CREATE VIEW IF NOT EXISTS "` + ViewRowMeta + `" AS
	WITH ent AS (
		SELECT r.source AS _source, r.tbl AS entity_table, r.id AS entity_id, m.key AS key_id,
			json_extract(m.value, '$.name') AS name, json_extract(m.value, '$.scope') AS scope,
			COALESCE(json_extract(m.value, '$.sensitive'), 0) AS sensitive, m.value AS entry
		FROM "` + tblRows + `" r, json_each(r.doc, '$.meta') m
		WHERE r.deleted = 0 AND json_valid(r.doc) AND json_type(r.doc, '$.meta') = 'object'
			AND json_type(r.doc, '$.deleted_at') IS NULL AND json_type(m.value) = 'object'
	),
	leaf AS (
		SELECT e.*, t.type AS jtype, t.atom AS atom, t.fullkey AS fullkey,
			CASE WHEN typeof(t.key) = 'integer' THEN t.key ELSE 0 END AS item_idx,
			ltrim(substr(CASE WHEN typeof(t.key) = 'integer' THEN t.path ELSE t.fullkey END, 4), '.') AS path
		FROM ent e, json_tree(e.entry, '$.v') t
		WHERE t.type NOT IN ('object', 'array')
	)
	SELECT _source, entity_table, entity_id, key_id, name AS key_name, scope, sensitive,
		'' AS path, -1 AS item_idx, NULL AS value_text, NULL AS value_fold, NULL AS value_num,
		NULL AS value_bool, NULL AS value_id, json_extract(entry, '$.v') AS value_json
	FROM ent
	UNION ALL
	SELECT _source, entity_table, entity_id, key_id,
		CASE WHEN path = '' THEN name ELSE name || '.' || path END,
		scope, sensitive, path, item_idx,
		vt, lower(vt),
		CASE WHEN jtype IN ('integer', 'real') THEN atom END,
		CASE jtype WHEN 'true' THEN 1 WHEN 'false' THEN 0 END,
		vid, NULL
	FROM (SELECT *,
		CASE WHEN jtype = 'text' AND atom LIKE 'val\_%' ESCAPE '\' THEN atom END AS vid,
		CASE jtype WHEN 'true' THEN 'true' WHEN 'false' THEN 'false' WHEN 'null' THEN NULL
			ELSE COALESCE(CASE WHEN jtype = 'text' AND atom LIKE 'val\_%' ESCAPE '\'
				THEN json_extract(entry, '$.v_name' || substr(fullkey, 4)) END, CAST(atom AS TEXT)) END AS vt
		FROM leaf)`,
}

func init() { internalDDL = append(internalDDL, rowMetaDDL...) }

// metaKeySensitiveQuery tells whether a cache's meta_keys table marks one
// key id sensitive (constant identifiers only).
const metaKeySensitiveQuery = `SELECT COUNT(*) FROM "` + schema.TableMetaKeys + `" WHERE id = ? AND "` + fieldSensitive + `" = 1`

// sensitiveMetaKeys reads which of r's meta key ids the registry in tx
// marks sensitive; a cache without the meta_keys table marks none (the
// entry's own sensitive flag still applies, see metaText).
func sensitiveMetaKeys(ctx context.Context, tx *sql.Tx, r row.Row) (map[string]bool, error) {
	out := map[string]bool{}
	if len(r.Meta) == 0 {
		return out, nil
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, schema.TableMetaKeys).Scan(&n); err != nil {
		return nil, dbErr("find meta_keys", err)
	}
	if n == 0 {
		return out, nil
	}
	for id := range r.Meta {
		var c int
		if err := tx.QueryRowContext(ctx, metaKeySensitiveQuery, id.String()).Scan(&c); err != nil {
			return nil, dbErr("read sensitive meta keys", err)
		}
		out[id.String()] = c > 0
	}
	return out, nil
}

// metaText is the searchable text of r's metadata (3.17 step 1): every
// string, number and date leaf of each value, an allowed value by its name,
// separated by spaces. Left out (3.20, 3.21): any entry the row or the
// registry marks sensitive (fail closed: either says so), an entry that
// does not decode, and any text holding a ${secret:…} reference. Key names
// are not indexed (they are reached with name:value).
func metaText(r row.Row, sensitive map[string]bool) string {
	keys := make([]string, 0, len(r.Meta))
	for k := range r.Meta {
		keys = append(keys, k.String())
	}
	slices.Sort(keys)
	var parts []string
	for _, k := range keys {
		for id, j := range r.Meta {
			if id.String() != k {
				continue
			}
			e, err := meta.DecodeEntry(j)
			if err != nil || e.Sensitive || sensitive[k] {
				continue
			}
			src := e.V
			if len(e.VName) > 0 {
				src = e.VName // allowed values: the name, never the val_ id
			}
			parts = appendLeaves(parts, src)
		}
	}
	return strings.Join(parts, " ")
}

// appendLeaves appends the string and number leaves of the JSON doc raw.
func appendLeaves(parts []string, raw []byte) []string {
	var v json.RawMessage = raw
	var s string
	if json.Unmarshal(v, &s) == nil {
		if s != "" && !strings.Contains(s, "${secret:") {
			parts = append(parts, s)
		}
		return parts
	}
	var list []json.RawMessage
	if json.Unmarshal(v, &list) == nil {
		for _, item := range list {
			parts = appendLeaves(parts, item)
		}
		return parts
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(v, &obj) == nil {
		names := make([]string, 0, len(obj))
		for n := range obj {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			parts = appendLeaves(parts, obj[n])
		}
		return parts
	}
	var num json.Number
	if json.Unmarshal(v, &num) == nil {
		parts = append(parts, num.String())
	}
	return parts // true/false/null: not text to find
}

// withMeta is vals plus the FTS meta text of r (an empty text adds nothing).
func withMeta(ctx context.Context, tx *sql.Tx, vals map[string]value.Value, r row.Row) (map[string]value.Value, error) {
	sensitive, err := sensitiveMetaKeys(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	text := metaText(r, sensitive)
	if text == "" {
		return vals, nil
	}
	// JSON-decoded strings are valid UTF-8, so this never refuses.
	t, err := value.TextOf(text)
	if err != nil {
		return nil, err
	}
	out := make(map[string]value.Value, len(vals)+1)
	for k, v := range vals {
		out[k] = v
	}
	out[ftsMeta] = t
	return out, nil
}

// metaRowsOfKeyQuery finds the live rows of every source whose meta holds a
// key id (bound as the JSON path '$.meta."<id>"').
const metaRowsOfKeyQuery = `SELECT source, doc FROM "` + tblRows + `" WHERE deleted = 0 AND json_valid(doc) AND json_type(doc, ?) IS NOT NULL ORDER BY tbl, id`

// reindexMetaKey re-projects every row (of any source in the file: an
// org key may describe repo rows) that uses meta key keyID, so a key turning sensitive drops its values from the FTS meta column
// (3.20) and one turning non-sensitive adds them back.
func (c *Cache) reindexMetaKey(ctx context.Context, tx *sql.Tx, st state, keyID string) error {
	rs, err := tx.QueryContext(ctx, metaRowsOfKeyQuery, `$.meta."`+keyID+`"`)
	if err != nil {
		return dbErr("find rows of meta key", err)
	}
	type heldRow struct {
		source store.Source
		sr     store.Row
	}
	var held []heldRow
	for rs.Next() {
		var source string
		var doc []byte
		if err := rs.Scan(&source, &doc); err != nil {
			return errors.Join(dbErr("find rows of meta key", err), rs.Close())
		}
		r, derr := row.Decode(doc)
		if derr != nil {
			continue // a newer _v: kept in _rows only (see project)
		}
		if sr, ferr := store.FromRow(r); ferr == nil {
			held = append(held, heldRow{store.Source(source), sr})
		}
	}
	if err := errors.Join(rs.Err(), rs.Close()); err != nil {
		return dbErr("find rows of meta key", err)
	}
	for _, h := range held {
		if h.sr.Table == schema.TableMetaKeys {
			continue // never recurse through a key row
		}
		if err := c.project(ctx, tx, st, h.source, h.sr); err != nil {
			return err
		}
	}
	return nil
}

// sensitiveKeysQuery lists the id of every sensitive meta key.
const sensitiveKeysQuery = `SELECT DISTINCT id FROM "` + schema.TableMetaKeys + `" WHERE "` + fieldSensitive + `" = 1 ORDER BY 1`

// reindexSensitiveKeys re-projects the rows of every sensitive meta key.
func (c *Cache) reindexSensitiveKeys(ctx context.Context, tx *sql.Tx, st state) error {
	if _, ok := st.plans[schema.TableMetaKeys]; !ok {
		return nil
	}
	rs, err := tx.QueryContext(ctx, sensitiveKeysQuery)
	if err != nil {
		return dbErr("read sensitive meta keys", err)
	}
	var keys []string
	for rs.Next() {
		var k string
		if err := rs.Scan(&k); err != nil {
			return errors.Join(dbErr("read sensitive meta keys", err), rs.Close())
		}
		keys = append(keys, k)
	}
	if err := errors.Join(rs.Err(), rs.Close()); err != nil {
		return dbErr("read sensitive meta keys", err)
	}
	for _, k := range keys {
		if err := c.reindexMetaKey(ctx, tx, st, k); err != nil {
			return err
		}
	}
	return nil
}
