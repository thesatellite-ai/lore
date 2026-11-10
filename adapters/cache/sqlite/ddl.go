package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Internal column names of every typed table. They start with an
// underscore or are envelope keys, so no FieldDef can take them
// (row.ValidName / row.ReservedName).
const (
	colID     = row.KeyID
	colSource = "_source"
	colHLC    = row.KeyHLC
)

// auditColumns are the envelope audit keys copied into every typed table.
var auditColumns = []string{row.KeyCreatedAt, row.KeyCreatedBy, row.KeyUpdatedAt, row.KeyUpdatedBy}

// Derived object names.
const (
	ftsPrefix    = "_fts_"
	ftsColPrefix = "f_"
	indexPrefix  = "ix_"
	// indexSep joins a table name and the index's position: the part after
	// the last separator is all digits, so "ix_<table>__<n>" can never
	// collide between two tables, whatever underscores their names hold.
	indexSep = "__"
)

// Join table columns (many-to-many).
const (
	joinFrom = "from_id"
	joinTo   = "to_id"
)

// SQLite declared types (see the affinity table in the package doc).
const (
	sqlText    = "TEXT"
	sqlNumeric = "NUMERIC"
	sqlInteger = "INTEGER"
)

// objType is the closed set of schema objects a plan creates besides its
// table's indexes (which go with the table).
type objType string

const (
	objTable objType = "table"
	objView  objType = "view"
	objJoin  objType = "join" // a join table: a table, listed apart for the lens
	objFTS   objType = "fts"  // an FTS5 virtual table: dropped like a table
)

// dbObject is one created object, recorded in `_tables.objects` so a later
// open can drop exactly what an older definition created.
type dbObject struct {
	Type objType `json:"type"`
	Name string  `json:"name"`
}

// plan is everything one TableDef becomes in the cache.
//
// Invariant: create holds only statements built from validated, quoted
// identifiers; fingerprint changes whenever any of them changes, which is
// what triggers a drop and refill.
type plan struct {
	def         schema.TableDef
	columns     []column // typed table columns in order, internal first
	fts         string   // FTS table name, "" when the def has no FTS fields
	view        string   // stable kind view name, "" for built-ins
	joins       []joinPlan
	create      []string
	sql         tableSQL
	objects     []dbObject
	fingerprint string
}

// tableSQL is every statement the cache runs against one typed table,
// built once in planFor from validated, quoted identifiers, so no SQL text
// is ever assembled at a call site or from row data.
type tableSQL struct {
	// insert replaces one typed row; ftsInsert adds one FTS row. Both take
	// a single bound parameter, a JSON array with one cell per column
	// (the columns in order; for FTS id, _source, then the FTS fields),
	// read back with json_extract: dynamic arity without untyped argument
	// slices.
	insert    string
	ftsInsert string
	// deleteID / ftsDeleteID take (source, row id).
	deleteID    string
	ftsDeleteID string
	// clearSource / ftsClearSource take the source; refsClearSource and
	// expandedClearSource take (table name, source).
	clearSource         string
	ftsClearSource      string
	refsClearSource     string
	expandedClearSource string
	// selectText maps a text field to "SELECT field WHERE id = ?" (the
	// first source's copy when two sources hold the id; references are
	// tracked per id, see deleteTyped).
	selectText map[string]string
	// search takes (match, limit).
	search string
}

// joinPlan is the cache-only join table of one many-to-many relation
// (decision O11): (from_id, to_id, _source), filled from the owning row's
// list-of-ref field on every upsert. Primary key (_source, from_id, to_id),
// so a list naming one id twice yields one pair and two sources keep their
// own pairs; both id columns are indexed.
type joinPlan struct {
	rel schema.Relation
	// insert takes (source, from id, to id) and ignores a duplicate pair.
	insert string
	// deleteFrom takes (source, from id); clearSource takes the source.
	deleteFrom  string
	clearSource string
}

// column is one typed-table column.
type column struct {
	name  string
	field schema.FieldDef // zero for internal columns
	typ   string
}

// affinity maps a field kind to its declared SQLite type.
func affinity(k value.Kind) (string, error) {
	switch k {
	case value.KindText, value.KindDate, value.KindDateTime, value.KindDuration,
		value.KindIP, value.KindCIDR, value.KindDNS, value.KindURL, value.KindEmail,
		value.KindRef, value.KindSecretRef, value.KindJSON, value.KindList:
		return sqlText, nil
	case value.KindNumber:
		return sqlNumeric, nil
	case value.KindBool:
		return sqlInteger, nil
	case value.KindNull:
		return "", errs.New(errs.InvalidInput, "sqlite cache: a field cannot be of kind null")
	}
	return "", errs.New(errs.InvalidInput, "sqlite cache: unknown field kind "+quoteText(string(k)))
}

// planFor builds the DDL of def. view is the stable view name of a kind
// table ("" for built-ins).
func planFor(def schema.TableDef, view string) (plan, error) {
	if def.Name == schema.TableResources && len(def.FTS) == 0 {
		def = resourceFTSDef(def)
	}
	def = metaFTSDef(def)
	p := plan{def: def, view: view}
	tq, err := ident(def.Name.String())
	if err != nil {
		return plan{}, err
	}
	p.columns = []column{{name: colID, typ: sqlText}, {name: colSource, typ: sqlText}, {name: colHLC, typ: sqlText}}
	for _, a := range auditColumns {
		p.columns = append(p.columns, column{name: a, typ: sqlText})
	}
	for _, f := range def.Fields {
		typ, kindErr := affinity(f.Kind)
		if kindErr != nil {
			return plan{}, kindErr
		}
		p.columns = append(p.columns, column{name: f.Name, field: f, typ: typ})
	}
	defs := make([]string, 0, len(p.columns))
	quoted := make([]string, 0, len(p.columns))
	for _, c := range p.columns {
		cq, colErr := ident(c.name)
		if colErr != nil {
			return plan{}, colErr
		}
		quoted = append(quoted, cq)
		d := cq + " " + c.typ
		switch c.name {
		case colID, colSource, colHLC:
			d += " NOT NULL"
		}
		defs = append(defs, d)
	}
	// The key is (_source, id), not id: one file may hold several sources
	// (the combined cache of CFG3), and an id held by two of them must stay
	// two rows so a reader can report E_AMBIGUOUS_ID instead of silently
	// showing whichever source applied last.
	defs = append(defs, `PRIMARY KEY ("`+colSource+`", "`+colID+`")`)
	p.create = append(p.create, "CREATE TABLE "+tq+" ("+strings.Join(defs, ", ")+")")
	p.sql.insert = "INSERT OR REPLACE INTO " + tq + " (" + strings.Join(quoted, ", ") + ") VALUES (" + extractList(len(quoted)) + ")"
	p.sql.deleteID = "DELETE FROM " + tq + ` WHERE "` + colSource + `" = ? AND "` + colID + `" = ?`
	p.sql.clearSource = "DELETE FROM " + tq + ` WHERE "` + colSource + `" = ?`
	bySource := ` WHERE entity_table = ? AND entity_id IN (SELECT "` + colID + `" FROM ` + tq + ` WHERE "` + colSource + `" = ?)`
	p.sql.refsClearSource = `DELETE FROM "` + tblRefs + `"` + bySource
	p.sql.expandedClearSource = `DELETE FROM "` + tblExpanded + `"` + bySource
	p.sql.selectText = map[string]string{}
	for i, c := range p.columns {
		if c.field.Kind == value.KindText {
			p.sql.selectText[c.name] = "SELECT " + quoted[i] + " FROM " + tq + ` WHERE "` + colID + `" = ? ORDER BY "` + colSource + `" LIMIT 1`
		}
	}
	p.objects = append(p.objects, dbObject{Type: objTable, Name: def.Name.String()})

	idx, err := indexStatements(def)
	if err != nil {
		return plan{}, err
	}
	p.create = append(p.create, idx...)

	if err := p.addFTS(); err != nil {
		return plan{}, err
	}
	if err := p.addJoins(); err != nil {
		return plan{}, err
	}
	if view != "" {
		vq, err := ident(view)
		if err != nil {
			return plan{}, err
		}
		p.create = append(p.create, "CREATE VIEW "+vq+" AS SELECT * FROM "+tq)
		p.objects = append(p.objects, dbObject{Type: objView, Name: view})
	}
	// A kind field turning sensitive must refill the kind table, because
	// that refill (upsertKind → indexResource) is what drops its values
	// from the resources full-text index (RES 3.12: sensitive values are
	// never indexed); the DDL alone does not change.
	var sensitive []string
	for _, f := range def.Fields {
		if f.Sensitive {
			sensitive = append(sensitive, f.Name)
		}
	}
	sum := sha256.Sum256([]byte("layout " + strconv.Itoa(layoutVersion) + "\n" + strings.Join(p.create, ";\n") + "\nsensitive " + strings.Join(sensitive, ",")))
	p.fingerprint = hex.EncodeToString(sum[:])
	return p, nil
}

// indexStatements returns the CREATE INDEX statements of def: its declared
// indexes (never UNIQUE, see the package doc), one per ref field, one per
// polymorphic (type, id) pair and one on _source (Rebuild deletes by it).
// Duplicate field lists are created once.
func indexStatements(def schema.TableDef) ([]string, error) {
	var lists [][]string
	add := func(fields ...string) {
		for _, l := range lists {
			if slices.Equal(l, fields) {
				return
			}
		}
		lists = append(lists, fields)
	}
	add(colSource)
	add(colID)
	for _, ix := range def.Indexes {
		add(ix.Fields...)
	}
	for _, f := range def.Fields {
		if f.Kind == value.KindRef {
			add(f.Name)
		}
	}
	for _, rel := range def.Relations {
		switch rel.Kind {
		case schema.Polymorphic:
			add(rel.TypeField, rel.Field)
		case schema.BelongsTo, schema.Self:
			add(rel.Field)
		case schema.HasMany, schema.ManyToMany:
			// HasMany's field lives on the target (indexed there as a ref
			// field); ManyToMany is indexed in its join table.
		}
	}
	tq, err := ident(def.Name.String())
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(lists))
	for n, l := range lists {
		cols := make([]string, len(l))
		for i, f := range l {
			if cols[i], err = ident(f); err != nil {
				return nil, err
			}
		}
		nq, err := ident(indexPrefix + def.Name.String() + indexSep + strconv.Itoa(n))
		if err != nil {
			return nil, err
		}
		out = append(out, "CREATE INDEX "+nq+" ON "+tq+" ("+strings.Join(cols, ", ")+")")
	}
	return out, nil
}

// FTS settings, v0.1.17 parity (saas/pkg/aicoder/fts5): porter stemming
// over unicode61 with diacritics folded ("running" finds "run", "cafe"
// finds "café"), and the snippet v0.1.17 printed: matches in <b>…</b>,
// "..." where text is cut, about 32 tokens.
const (
	ftsTokenizer    = "porter unicode61 remove_diacritics 2"
	snippetOpen     = "<b>"
	snippetClose    = "</b>"
	snippetEllipsis = "..."
	snippetTokens   = 32
)

// addFTS adds the FTS5 table of p's FTS fields.
func (p *plan) addFTS() error {
	if len(p.def.FTS) == 0 {
		return nil
	}
	p.fts = ftsPrefix + p.def.Name.String()
	fq, err := ident(p.fts)
	if err != nil {
		return err
	}
	cols := []string{`"` + colID + `" UNINDEXED`, `"` + colSource + `" UNINDEXED`}
	names := []string{`"` + colID + `"`, `"` + colSource + `"`}
	// bm25 takes one weight per column, the UNINDEXED id and _source too
	// (they never match, so 0).
	weights := []string{"0", "0"}
	for _, f := range p.def.FTS {
		weights = append(weights, strconv.FormatFloat(p.def.FTSWeightOf(f), 'g', -1, 64))
		cq, err := ident(ftsColPrefix + f)
		if err != nil {
			return err
		}
		cols = append(cols, cq)
		names = append(names, cq)
	}
	p.create = append(p.create, "CREATE VIRTUAL TABLE "+fq+" USING fts5("+strings.Join(cols, ", ")+", tokenize='"+ftsTokenizer+"')")
	p.sql.ftsDeleteID = "DELETE FROM " + fq + ` WHERE "` + colSource + `" = ? AND "` + colID + `" = ?`
	p.sql.ftsClearSource = "DELETE FROM " + fq + ` WHERE "` + colSource + `" = ?`
	rank := "bm25(" + fq + ", " + strings.Join(weights, ", ") + ")"
	p.sql.search = `SELECT "` + colID + `", ` + rank + ", snippet(" + fq + ", -1, '" + snippetOpen + "', '" + snippetClose + "', '" + snippetEllipsis + "', " + strconv.Itoa(snippetTokens) + ") FROM " + fq +
		" WHERE " + fq + " MATCH ? ORDER BY " + rank + " LIMIT ?"
	p.sql.ftsInsert = "INSERT INTO " + fq + " (" + strings.Join(names, ", ") + ") VALUES (" + extractList(len(names)) + ")"
	p.objects = append(p.objects, dbObject{Type: objFTS, Name: p.fts})
	return nil
}

// addJoins adds the cache-only join table of every many-to-many relation
// (see joinPlan) with an index on each id column.
func (p *plan) addJoins() error {
	for _, rel := range p.def.Relations {
		if rel.Kind != schema.ManyToMany {
			continue
		}
		name := rel.JoinTable.String()
		jq, err := ident(name)
		if err != nil {
			return err
		}
		p.create = append(p.create,
			"CREATE TABLE "+jq+` ("`+joinFrom+`" TEXT NOT NULL, "`+joinTo+`" TEXT NOT NULL, "`+colSource+`" TEXT NOT NULL, PRIMARY KEY ("`+colSource+`", "`+joinFrom+`", "`+joinTo+`"))`)
		for n, c := range []string{joinFrom, joinTo} {
			nq, err := ident(indexPrefix + name + indexSep + strconv.Itoa(n))
			if err != nil {
				return err
			}
			p.create = append(p.create, "CREATE INDEX "+nq+" ON "+jq+` ("`+c+`")`)
		}
		p.joins = append(p.joins, joinPlan{
			rel:         rel,
			insert:      "INSERT OR IGNORE INTO " + jq + ` ("` + colSource + `", "` + joinFrom + `", "` + joinTo + `") VALUES (?, ?, ?)`,
			deleteFrom:  "DELETE FROM " + jq + ` WHERE "` + colSource + `" = ? AND "` + joinFrom + `" = ?`,
			clearSource: "DELETE FROM " + jq + ` WHERE "` + colSource + `" = ?`,
		})
		p.objects = append(p.objects, dbObject{Type: objJoin, Name: name})
	}
	return nil
}

// dropStatement returns the DROP of one recorded object (indexes go with
// their table).
func dropStatement(o dbObject) (string, error) {
	q, err := ident(o.Name)
	if err != nil {
		return "", err
	}
	switch o.Type {
	case objView:
		return "DROP VIEW IF EXISTS " + q, nil
	case objTable, objJoin, objFTS:
		return "DROP TABLE IF EXISTS " + q, nil
	}
	return "", errs.New(errs.DBCorrupt, "sqlite cache: unknown object type "+quoteText(string(o.Type)))
}

// Resource full text (RESOURCES 3.12, R8): the `resources` table gets an
// FTS table over its name plus two cache-only columns, the kind's name and
// the resource's non-sensitive field values (indexResource fills them).
// The schema declares no FTS for resources because the values live inside
// the `fields` object, which only the cache unpacks.
const (
	ftsResKind   = "kind_name"
	ftsResValues = "field_values"
	// ftsWeightResValues ranks a value hit between the name (4) and the
	// kind word (1), like the meta column's weight 2 (METADATA 3.17).
	ftsWeightResValues = 2
)

// resourceFTSDef is def (the resources table) with its full-text columns.
func resourceFTSDef(def schema.TableDef) schema.TableDef {
	def.FTS = []string{fieldName, ftsResKind, ftsResValues}
	def.FTSWeights = map[string]float64{ftsResValues: ftsWeightResValues}
	return def
}

// metaFTSDef is def with the meta column (METADATA 3.17 step 1) added to
// its full-text fields, weight 2; a def without full text is unchanged.
// The slices and map are copied: def is shared with the registry.
func metaFTSDef(def schema.TableDef) schema.TableDef {
	if len(def.FTS) == 0 || slices.Contains(def.FTS, ftsMeta) {
		return def
	}
	def.FTS = append(slices.Clone(def.FTS), ftsMeta)
	w := make(map[string]float64, len(def.FTSWeights)+1)
	for k, v := range def.FTSWeights {
		w[k] = v
	}
	w[ftsMeta] = ftsWeightMeta
	def.FTSWeights = w
	return def
}
