package wire

// modeb_v017.go — the ModeBExporter of the standalone binary (cmd/lore):
// it reads a retired v0.1.17 Mode B project out of its shared lore.db
// without the v0.1.17 code, so `lore migrate run` converts a Mode B repo
// on the binary that outlives the old one (Q-MODEB).
//
//	ExportModeB(pointer)
//	  parse .lore/lore.yaml (or the older lore.toml): db_path + project_id
//	  expand ${HOME} ${LORE_HOME} ${PROJECT_ROOT}; a ".." part refused
//	      unless LORE_ALLOW_DB_PATH_OUTSIDE=1 (v0.1.17 R27 #21)
//	  open the shared lore.db read-only (other repos may still use it)
//	  pick the project's rows, every synced non-machine-local table:
//	      pass 1  rows whose project_id / entity_project_id is the project
//	      pass 2  rows with no project value whose *_id columns point at a
//	              row pass 1 chose (rule_verifier_refs, comments, …)
//	  one `_v: 1` document per row, typed by the table definition
//	  entity_tags rows of the exported rows → TagLinks (O11)
//
// The documents carry the columns the 0.1.18 table defines (plus id,
// created_at, updated_at): exactly the fields the core keeps. v0.1.17
// columns that never travelled in a row file (embeddings, access counters,
// last_accessed_at) have no field and are left behind, as a v0.1.17 export
// left them. Pass 2 also takes a row whose project column is NULL when it
// hangs off an exported row (a v0.1.17 comment stores no
// entity_project_id), so such rows move with their parent.

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
)

// Names the v0.1.17 Mode B reader depends on.
const (
	// modeBAllowOutsideEnv lets db_path hold a ".." part (v0.1.17's
	// projresolve.EnvAllowDBPathOutside, same name and value).
	modeBAllowOutsideEnv = "LORE_ALLOW_DB_PATH_OUTSIDE"
	// v0.1.17 pointer placeholders (projresolve.ExpandPath).
	modeBVarHome        = "${HOME}"
	modeBVarLoreHome    = "${LORE_HOME}"
	modeBVarProjectRoot = "${PROJECT_ROOT}"
	// modeBLoreHomeEnv is the variable ${LORE_HOME} reads.
	modeBLoreHomeEnv = "LORE_HOME"
	// modeBLoreHomeDir is ${LORE_HOME}'s default under the home folder.
	modeBLoreHomeDir = ".lore"
	// modeBTOMLExt marks the older pointer form.
	modeBTOMLExt = ".toml"
	// v0.1.17 lore.db names the reader queries.
	v017ProjectsTable   = "projects"
	v017EntityTagsTable = "entity_tags"
	v017IDSuffix        = "_id"
	// v017DocTime is the time form a v0.1.17 row file holds (lsync
	// timeLayout): UTC, nine fraction digits.
	v017DocTime = "2006-01-02T15:04:05.000000000Z"
	// goMonotonicMark starts the monotonic suffix of Go's time.String(),
	// which ent wrote into some v0.1.17 columns.
	goMonotonicMark = " m="
)

// v017ProjectColumns place a v0.1.17 row in a project, in the order tried.
var v017ProjectColumns = []string{"project_id", "entity_project_id"}

// v017EnvelopeColumns are the columns every v0.1.17 row file carries
// beside its table's fields.
var v017EnvelopeColumns = []string{row.KeyID, row.KeyCreatedAt, row.KeyUpdatedAt}

// v017LooseTimes are the forms a v0.1.17 lore.db time cell holds: the row
// file form, RFC 3339, Go's time.String() (ent via modernc), SQLite's
// CURRENT_TIMESTAMP (lsync looseTimeLayouts).
var v017LooseTimes = []string{
	v017DocTime,
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999 -0700 -0700",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
}

// V017ModeB is the ModeBExporter cmd/lore installs (StandaloneOptions).
type V017ModeB struct{}

var _ ModeBExporter = V017ModeB{}

// modeBPointer is the content of a v0.1.17 pointer file.
type modeBPointer struct {
	DBPath    string `yaml:"db_path"`
	ProjectID string `yaml:"project_id"`
}

// ExportModeB implements ModeBExporter; see the file comment.
func (V017ModeB) ExportModeB(ctx context.Context, pointerPath string) (_ ModeBExport, err error) {
	ptr, err := readModeBPointerFile(pointerPath)
	if err != nil {
		return ModeBExport{}, err
	}
	root := filepath.Dir(filepath.Dir(pointerPath))
	dbPath, err := expandModeBPath(ptr.DBPath, root)
	if err != nil {
		return ModeBExport{}, err
	}
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(root, dbPath)
	}
	if _, serr := os.Stat(dbPath); serr != nil {
		return ModeBExport{}, errs.Wrap(errs.NotFound, "migrate: the shared lore.db "+dbPath+" named by "+pointerPath+" is not there", serr)
	}
	dsn := fileuri.URL(dbPath, "mode=ro")
	db, err := sql.Open(sqliteDriver, dsn)
	if err != nil {
		return ModeBExport{}, errs.Wrap(errs.Internal, "migrate: open "+dbPath, err)
	}
	defer func() { _ = db.Close() }() // read-only handle: nothing to flush
	out := ModeBExport{DBPath: dbPath, ProjectID: ptr.ProjectID}
	q := `SELECT name FROM "` + v017ProjectsTable + `" WHERE id = ?`
	if serr := db.QueryRowContext(ctx, q, ptr.ProjectID).Scan(&out.ProjectName); serr != nil {
		return ModeBExport{}, errs.Wrap(errs.NotFound, "migrate: project "+ptr.ProjectID+" is not in "+dbPath, serr)
	}
	reg, err := builtinRegistry()
	if err != nil {
		return ModeBExport{}, err
	}
	picked, err := modeBPickRows(ctx, db, reg.Tables(), ptr.ProjectID)
	if err != nil {
		return ModeBExport{}, err
	}
	exported := map[string]bool{}
	for _, p := range picked {
		for _, doc := range p.docs {
			out.Docs = append(out.Docs, doc.raw)
			exported[doc.id] = true
		}
	}
	if out.TagLinks, err = v017TagLinks(ctx, db, exported); err != nil {
		return ModeBExport{}, err
	}
	return out, nil
}

// readModeBPointerFile parses lore.yaml or the older lore.toml, without
// changing it (a dry run changes nothing); both keys are required.
func readModeBPointerFile(path string) (modeBPointer, error) {
	var p modeBPointer
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return p, errs.Wrap(errs.Internal, "migrate: read "+path, err)
	}
	if strings.HasSuffix(path, modeBTOMLExt) {
		p, err = parseModeBTOML(b)
	} else {
		err = yamlerr.Decode(b, &p, yamlerr.Lenient) // line + key, never a Go type
	}
	if err == nil && (p.DBPath == "" || p.ProjectID == "") {
		err = errors.New("db_path and project_id are both required")
	}
	if err != nil {
		return modeBPointer{}, errs.Wrap(errs.InvalidInput, "migrate: "+path+" is not a Mode B pointer: "+err.Error(), err)
	}
	return p, nil
}

// parseModeBTOML reads the two-key TOML pointer v0.1.17 once wrote
// (`db_path = "…"`, `project_id = "…"`): blank lines, # comments, basic
// or literal strings. Anything else is refused rather than guessed.
func parseModeBTOML(b []byte) (modeBPointer, error) {
	var p modeBPointer
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			return p, errors.New("line " + strconv.Itoa(n) + ": not key = value")
		}
		val, err := tomlString(strings.TrimSpace(raw))
		if err != nil {
			return p, errors.New("line " + strconv.Itoa(n) + ": " + err.Error())
		}
		switch strings.TrimSpace(key) {
		case "db_path":
			p.DBPath = val
		case "project_id":
			p.ProjectID = val
		}
	}
	return p, sc.Err()
}

// tomlString decodes a TOML basic ("…", JSON-compatible escapes) or
// literal ('…') string, with an optional trailing # comment.
func tomlString(s string) (string, error) {
	switch {
	case strings.HasPrefix(s, "'"):
		end := strings.Index(s[1:], "'")
		if end < 0 || !tomlTailOK(s[end+2:]) {
			return "", errors.New("bad literal string")
		}
		return s[1 : end+1], nil
	case strings.HasPrefix(s, `"`):
		dec := json.NewDecoder(strings.NewReader(s))
		var out string
		if err := dec.Decode(&out); err != nil {
			return "", errors.New("bad string")
		}
		if !tomlTailOK(s[dec.InputOffset():]) {
			return "", errors.New("text after the string")
		}
		return out, nil
	}
	return "", errors.New("value is not a string")
}

// tomlTailOK accepts what may follow a TOML value: spaces and a comment.
func tomlTailOK(rest string) bool {
	rest = strings.TrimSpace(rest)
	return rest == "" || strings.HasPrefix(rest, "#")
}

// expandModeBPath is v0.1.17's projresolve.ExpandPath: ${HOME},
// ${LORE_HOME} (default <home>/.lore) and ${PROJECT_ROOT}, then a ".."
// segment is refused unless LORE_ALLOW_DB_PATH_OUTSIDE=1.
func expandModeBPath(p, projectRoot string) (string, error) {
	out := p
	if strings.Contains(out, modeBVarHome) || (strings.Contains(out, modeBVarLoreHome) && os.Getenv(modeBLoreHomeEnv) == "") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errs.Wrap(errs.Internal, "migrate: expand "+p, err)
		}
		out = strings.ReplaceAll(out, modeBVarHome, home)
		out = strings.ReplaceAll(out, modeBVarLoreHome, filepath.Join(home, modeBLoreHomeDir))
	}
	out = strings.ReplaceAll(out, modeBVarLoreHome, os.Getenv(modeBLoreHomeEnv))
	out = strings.ReplaceAll(out, modeBVarProjectRoot, projectRoot)
	if os.Getenv(modeBAllowOutsideEnv) != envOn && slices.Contains(strings.Split(filepath.ToSlash(out), "/"), "..") {
		return "", errs.New(errs.BadPath, "migrate: db_path "+strconv.Quote(p)+" has a `..` part").
			WithHint("set " + modeBAllowOutsideEnv + "=1 to allow it")
	}
	// The OS's separators: a pointer written C:/shared/lore.db on Windows
	// names C:\shared\lore.db (a no-op elsewhere).
	return filepath.FromSlash(out), nil
}

// v017Doc is one exported row document.
type v017Doc struct {
	id  string
	raw []byte
}

// v017Picked is one table's exported rows.
type v017Picked struct {
	def  schema.TableDef
	docs []v017Doc
}

// modeBPickRows runs the two passes of the file comment over defs (sorted
// by name, so the dry run lists rows in a stable order).
func modeBPickRows(ctx context.Context, db *sql.DB, defs []schema.TableDef, projectID string) ([]v017Picked, error) {
	defs = slices.Clone(defs)
	slices.SortFunc(defs, func(a, b schema.TableDef) int { return strings.Compare(string(a.Name), string(b.Name)) })
	type tbl struct {
		def     schema.TableDef
		cols    map[string]bool
		projCol string
	}
	var tables []tbl
	for _, def := range defs {
		if !def.Synced || def.MachineLocal || !row.ValidName(string(def.Name)) {
			continue
		}
		cols, err := v017Columns(ctx, db, string(def.Name))
		if err != nil {
			return nil, err
		}
		if !cols[row.KeyID] {
			continue // not in this lore.db (an older or newer schema)
		}
		t := tbl{def: def, cols: cols}
		for _, c := range v017ProjectColumns {
			if cols[c] {
				t.projCol = c
				break
			}
		}
		tables = append(tables, t)
	}
	chosen := map[string]bool{}
	out := make([]v017Picked, len(tables))
	for i, t := range tables {
		out[i].def = t.def
		if t.projCol == "" {
			continue
		}
		docs, err := v017ReadDocs(ctx, db, t.def, t.cols, `"`+t.projCol+`" = ?`, projectID)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			chosen[d.id] = true
		}
		out[i].docs = docs
	}
	for i, t := range tables {
		var refs []string
		for c := range t.cols {
			if c != row.KeyID && c != t.projCol && strings.HasSuffix(c, v017IDSuffix) && row.ValidName(c) {
				refs = append(refs, c)
			}
		}
		if len(refs) == 0 {
			continue
		}
		slices.Sort(refs)
		where := make([]string, len(refs))
		for j, c := range refs {
			where[j] = `"` + c + `" IN (SELECT value FROM json_each(?1))`
		}
		cond := "(" + strings.Join(where, " OR ") + ")"
		if t.projCol != "" {
			cond = `"` + t.projCol + `" IS NULL AND ` + cond
		}
		ids := make([]string, 0, len(chosen))
		for id := range chosen {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		list, err := json.Marshal(ids)
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "migrate: encode ids", err)
		}
		docs, err := v017ReadDocs(ctx, db, t.def, t.cols, cond, string(list))
		if err != nil {
			return nil, err
		}
		out[i].docs = append(out[i].docs, docs...)
	}
	return out, nil
}

// v017ReadDocs reads def's rows matching where as `_v: 1` row documents,
// ordered by id; where names its one argument arg as ?1 (as often as it
// needs). Identifiers are written into the statement: each is a TableDef or lore.db column name that passed
// row.ValidName and was found in lore.db's own table_info; no row data
// reaches the statement.
func v017ReadDocs(ctx context.Context, db *sql.DB, def schema.TableDef, cols map[string]bool, where, arg string) ([]v017Doc, error) {
	q, fields := v017DocQuery(def, cols, where)
	rows, err := db.QueryContext(ctx, q, arg)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: read "+string(def.Name), err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	var out []v017Doc
	for rows.Next() {
		var cell string
		if err := rows.Scan(&cell); err != nil {
			return nil, errs.Wrap(errs.Internal, "migrate: read "+string(def.Name), err)
		}
		d, err := v017RowDoc(def, fields, []byte(cell))
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: read "+string(def.Name), err)
	}
	return out, nil
}

// v017DocQuery is the statement v017ReadDocs runs (one json_object per
// row, so every cell comes back typed JSON) and the fields it reads: the
// def's fields lore.db has a column for.
func v017DocQuery(def schema.TableDef, cols map[string]bool, where string) (string, []schema.FieldDef) {
	var fields []schema.FieldDef
	pairs := make([]string, 0, len(def.Fields)+len(v017EnvelopeColumns))
	for _, c := range v017EnvelopeColumns {
		if cols[c] {
			pairs = append(pairs, "'"+c+`', "`+c+`"`)
		}
	}
	for _, f := range def.Fields {
		if !cols[f.Name] || slices.Contains(v017EnvelopeColumns, f.Name) || !row.ValidName(f.Name) {
			continue
		}
		fields = append(fields, f)
		pairs = append(pairs, "'"+f.Name+`', "`+f.Name+`"`)
	}
	return "SELECT json_object(" + strings.Join(pairs, ", ") + `) FROM "` + string(def.Name) + `" WHERE ` + where + " ORDER BY id", fields
}

// v017RowDoc turns one json_object cell into a `_v: 1` row document:
// envelope times in the row-file form, each field typed by its kind.
func v017RowDoc(def schema.TableDef, fields []schema.FieldDef, cell []byte) (v017Doc, error) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(cell, &in); err != nil {
		return v017Doc{}, errs.Wrap(errs.Internal, "migrate: read "+string(def.Name), err)
	}
	var id string
	if err := json.Unmarshal(in[row.KeyID], &id); err != nil || id == "" {
		return v017Doc{}, errs.New(errs.InvalidID, "migrate: a row of "+string(def.Name)+" has no id")
	}
	doc := map[string]json.RawMessage{
		row.KeyVersion: json.RawMessage("1"),
		row.KeyTable:   mustJSONString(string(def.Name)),
		row.KeyID:      in[row.KeyID],
	}
	for _, c := range []string{row.KeyCreatedAt, row.KeyUpdatedAt} {
		if raw, ok := in[c]; ok {
			v, err := v017Cell(value.KindDateTime, raw)
			if err != nil {
				return v017Doc{}, errs.Wrap(errs.InvalidInput, "migrate: "+string(def.Name)+"/"+id+" "+c, err)
			}
			doc[c] = v
		}
	}
	for _, f := range fields {
		v, err := v017Cell(f.Kind, in[f.Name])
		if err != nil {
			return v017Doc{}, errs.Wrap(errs.InvalidInput, "migrate: "+string(def.Name)+"/"+id+" "+f.Name, err)
		}
		doc[f.Name] = v
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return v017Doc{}, errs.Wrap(errs.Internal, "migrate: encode "+string(def.Name)+"/"+id, err)
	}
	return v017Doc{id: id, raw: raw}, nil
}

// mustJSONString is s as a JSON string (a string always encodes).
func mustJSONString(s string) json.RawMessage {
	b, _ := json.Marshal(s) // a Go string always marshals
	return b
}

// v017Cell converts one lore.db cell (as json_object typed it: NULL,
// number or text) to its row-file JSON for a field of kind k. ent kept
// bools as 0/1, times as text, JSON and list columns as JSON text.
func v017Cell(k value.Kind, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("null"), nil
	}
	isText := raw[0] == '"'
	var s string
	if isText {
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
	}
	switch k {
	case value.KindBool:
		txt := string(raw)
		if isText {
			txt = s
		}
		b, err := strconv.ParseBool(txt)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(strconv.FormatBool(b)), nil
	case value.KindNumber:
		if isText {
			if _, err := strconv.ParseFloat(s, 64); err != nil {
				return nil, err
			}
			return json.RawMessage(s), nil
		}
		return raw, nil
	case value.KindDateTime:
		if !isText {
			return nil, errors.New("a time is text in lore.db, got " + string(raw))
		}
		t, err := v017ParseTime(s)
		if err != nil {
			return nil, err
		}
		return mustJSONString(t.UTC().Format(v017DocTime)), nil
	case value.KindJSON, value.KindList:
		if !isText {
			return raw, nil // a bare number is valid JSON
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, []byte(s)); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case value.KindText, value.KindDate, value.KindDuration, value.KindIP, value.KindCIDR, value.KindDNS,
		value.KindURL, value.KindEmail, value.KindRef, value.KindNull, value.KindSecretRef:
		// text kinds: ent stored them as TEXT; their 0.1.18 checks run when
		// the migration validates the row
	}
	if isText {
		return raw, nil
	}
	return mustJSONString(string(raw)), nil // a numeric cell of a text column keeps its digits
}

// v017ParseTime parses a lore.db time cell in any of v017LooseTimes,
// after dropping Go's monotonic suffix.
func v017ParseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, goMonotonicMark); i >= 0 {
		s = s[:i]
	}
	for _, l := range v017LooseTimes {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("not a time: " + strconv.Quote(s))
}

// v017TagLinks reads the entity_tags rows of the exported rows (none when
// lore.db has no such table).
func v017TagLinks(ctx context.Context, db *sql.DB, exported map[string]bool) ([]opsmemory.TagLink, error) {
	cols, err := v017Columns(ctx, db, v017EntityTagsTable)
	if err != nil || len(cols) == 0 {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT entity_table, entity_id, tag_id FROM "`+v017EntityTagsTable+`" ORDER BY entity_id, tag_id`)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: list "+v017EntityTagsTable, err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	var out []opsmemory.TagLink
	for rows.Next() {
		var l opsmemory.TagLink
		if err := rows.Scan(&l.EntityTable, &l.EntityID, &l.TagID); err != nil {
			return nil, errs.Wrap(errs.Internal, "migrate: list "+v017EntityTagsTable, err)
		}
		if exported[l.EntityID] {
			out = append(out, l)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: list "+v017EntityTagsTable, err)
	}
	return out, nil
}

// ExportRepoDB implements RepoDBExporter: the rows of a checkout's own
// v0.1.17 lore.db (Mode A), for a repo whose rows never reached
// .lore/data (v0.1.17 with LORE_SYNC=0, or a repo from before row files):
// the project's rows as ExportModeB picks them, plus the project's own
// row and the actor rows they name, which a repo store holds and a Mode
// B folder store does not. projectID "" means the database's only
// project; a database with several is refused naming them.
func (V017ModeB) ExportRepoDB(ctx context.Context, dbPath, projectID string) (_ ModeBExport, err error) {
	db, err := sql.Open(sqliteDriver, fileuri.URL(dbPath, "mode=ro"))
	if err != nil {
		return ModeBExport{}, errs.Wrap(errs.Internal, "migrate: open "+dbPath, err)
	}
	defer func() { _ = db.Close() }() // read-only handle: nothing to flush
	if projectID == "" {
		if projectID, err = v017OnlyProject(ctx, db, dbPath); err != nil {
			return ModeBExport{}, err
		}
	}
	out := ModeBExport{DBPath: dbPath, ProjectID: projectID}
	reg, err := builtinRegistry()
	if err != nil {
		return ModeBExport{}, err
	}
	picked, err := modeBPickRows(ctx, db, reg.Tables(), projectID)
	if err != nil {
		return ModeBExport{}, err
	}
	exported := map[string]bool{}
	actors := map[string]bool{}
	for _, p := range picked {
		for _, doc := range p.docs {
			out.Docs = append(out.Docs, doc.raw)
			exported[doc.id] = true
			for _, a := range v017ActorRefs(doc.raw) {
				actors[a] = true
			}
		}
	}
	for table, want := range map[string][]string{v017ProjectsTable: {projectID}, string(schema.TableActors): slices.Sorted(maps.Keys(actors))} {
		def, ok := reg.Table(row.Table(table))
		if !ok || len(want) == 0 {
			continue
		}
		cols, cerr := v017Columns(ctx, db, table)
		if cerr != nil {
			return ModeBExport{}, cerr
		}
		if !cols[row.KeyID] {
			continue
		}
		list, jerr := json.Marshal(want)
		if jerr != nil {
			return ModeBExport{}, errs.Wrap(errs.Internal, "migrate: encode ids", jerr)
		}
		docs, rerr := v017ReadDocs(ctx, db, def, cols, `"`+row.KeyID+`" IN (SELECT value FROM json_each(?1))`, string(list))
		if rerr != nil {
			return ModeBExport{}, rerr
		}
		for _, d := range docs {
			if !exported[d.id] {
				out.Docs = append(out.Docs, d.raw)
				exported[d.id] = true
			}
		}
	}
	if out.TagLinks, err = v017TagLinks(ctx, db, exported); err != nil {
		return ModeBExport{}, err
	}
	return out, nil
}

// v017OnlyProject is the one project of a checkout's lore.db; none or
// several is E_INVALID_INPUT naming what is there.
func v017OnlyProject(ctx context.Context, db *sql.DB, dbPath string) (string, error) {
	cols, err := v017Columns(ctx, db, v017ProjectsTable)
	if err != nil {
		return "", err
	}
	if !cols[row.KeyID] {
		return "", errs.New(errs.InvalidInput, "migrate: "+dbPath+" has no projects table, so its rows belong to no project; nothing imported").
			WithHint("this is not a v0.1.17 lore.db; move it aside if nothing in it is needed")
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM "`+v017ProjectsTable+`" ORDER BY id`)
	if err != nil {
		return "", errs.Wrap(errs.Internal, "migrate: list the projects of "+dbPath, err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	var found []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", errs.Wrap(errs.Internal, "migrate: list the projects of "+dbPath, err)
		}
		found = append(found, id)
	}
	if err := rows.Err(); err != nil {
		return "", errs.Wrap(errs.Internal, "migrate: list the projects of "+dbPath, err)
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", errs.New(errs.InvalidInput, "migrate: "+dbPath+" holds no project, so there is nothing of this repo's to import").
			WithHint("move the file aside if nothing in it is needed")
	default:
		return "", errs.New(errs.InvalidInput, "migrate: "+dbPath+" holds "+strconv.Itoa(len(found))+" projects ("+strings.Join(found, ", ")+") and .lore/data/_meta.json names none, so lore cannot tell which is this repo's; nothing imported").
			WithHint(`write {"_v": 1, "project_id": "<one of them>"} to .lore/data/_meta.json, then run ` + "`lore migrate run`" + ` again`)
	}
}

// v017ActorRefs lists the act_ ids a v0.1.17 row document names in its
// *_id fields (created_by_actor_id, validated_by_actor_id, …).
func v017ActorRefs(doc []byte) []string {
	var m map[string]json.RawMessage
	if json.Unmarshal(doc, &m) != nil {
		return nil
	}
	var out []string
	for k, raw := range m {
		if !strings.HasSuffix(k, v017IDSuffix) {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.HasPrefix(s, string(ids.PrefixActor)+"_") {
			out = append(out, s)
		}
	}
	return out
}

// convertedModeBProjectID is the project_id of the pointer a converted
// Mode B repo kept (.lore/lore.yaml.migrated or lore.toml.migrated),
// ok=false when the repo has none (owner item 36: the converted project's
// row lives in the "shared" folder store, never in the repo store, so the
// repo's project is found through this id).
func convertedModeBProjectID(top string) (string, bool) {
	for _, n := range modeBPointerNames {
		b, err := os.ReadFile(filepath.Clean(filepath.Join(top, modeBDir, n+ModeBMigratedSuffix)))
		if err != nil {
			continue
		}
		var p modeBPointer
		if strings.HasSuffix(n, modeBTOMLExt) {
			p, err = parseModeBTOML(b)
		} else {
			err = yamlerr.Decode(b, &p, yamlerr.Lenient)
		}
		if err == nil && p.ProjectID != "" {
			return p.ProjectID, true
		}
	}
	return "", false
}
