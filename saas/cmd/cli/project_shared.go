// project_shared.go — the retired v0.1.17 Mode B (decision Q-MODEB).
//
// Mode B let several project roots share one lore.db: each root held a
// pointer (.lore/lore.yaml, older .lore/lore.toml) naming the shared file
// and a project_id. 0.1.18 replaces it with stores, so:
//
//	lore project shared-init|shared-list
//	    refuse (E_UNSUPPORTED), naming the replacement:
//	    `lore store init <name> file:///abs/path` + `lore store add`.
//	    shared-list refuses too rather than listing: a list from a file
//	    0.1.18 no longer reads would suggest the mode still works.
//
//	lore migrate run
//	    converts an existing Mode B repo (wire/migrate_modeb.go); this file
//	    supplies the half that reads the shared lore.db with the v0.1.17
//	    row codec (modeBExporter).
package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dbent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/constants"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/wire"
)

// modeBReplacement is the refusal's hint: the stores commands that replace
// a shared database.
const modeBReplacement = "the shared-database mode is retired; use a store instead: " +
	"`lore store init <name> file:///abs/path` once, then `lore store add <name> <url>` in each repo " +
	"(write to it with `--to <name>`); an existing shared project converts with `lore migrate run`"

// newProjectSharedInitCommand is the retired `lore project shared-init`.
func newProjectSharedInitCommand() *cobra.Command {
	return retiredModeBCommand("shared-init", "Retired (0.1.18): use `lore store init` + `lore store add`")
}

// newProjectSharedListCommand is the retired `lore project shared-list`.
func newProjectSharedListCommand() *cobra.Command {
	return retiredModeBCommand("shared-list", "Retired (0.1.18): use `lore store list`")
}

// retiredModeBCommand refuses whatever it is given; the v0.1.17 flags are
// still declared so an old invocation gets the refusal, not "unknown flag".
func retiredModeBCommand(use, short string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ArbitraryArgs,
		RunE: func(*cobra.Command, []string) error {
			return errcodes.New(errcodes.Unsupported, "lore project "+use+" was removed in 0.1.18").
				WithHint(modeBReplacement)
		},
	}
	var ignored string
	var ignoredBool bool
	cmd.Flags().StringVar(&ignored, constants.FlagDB, "", "ignored (retired)")
	cmd.Flags().StringVar(&ignored, constants.FlagName, "", "ignored (retired)")
	cmd.Flags().BoolVar(&ignoredBool, constants.FlagJSON, false, "ignored (retired)")
	return cmd
}

// retiredModeBError is the refusal the v0.1.17 command paths give in a repo
// that still has a Mode B pointer (the same sentence wire gives).
func retiredModeBError(pointer string) error {
	return errcodes.New(errcodes.Unsupported, wire.RetiredModeBMessage(pointer)).WithHint(wire.RetiredModeBHint)
}

// modeBExporter implements wire.ModeBExporter with the v0.1.17 code: the
// pointer is parsed in place (no lorecfg conversion, so a dry run changes
// nothing) and every row is read with lsync.RowDoc, the bytes a v0.1.17
// export writes.
type modeBExporter struct{}

var _ wire.ModeBExporter = modeBExporter{}

// Columns that place a v0.1.17 row in a project.
const (
	colProjectID       = "project_id"
	colEntityProjectID = "entity_project_id"
	colID              = "id"
	idColSuffix        = "_id"
	entityTagsTable    = "entity_tags"
	projectsTable      = "projects"
)

// ExportModeB implements wire.ModeBExporter.
func (modeBExporter) ExportModeB(ctx context.Context, pointerPath string) (_ wire.ModeBExport, err error) {
	ptr, err := readModeBPointer(pointerPath)
	if err != nil {
		return wire.ModeBExport{}, err
	}
	root := filepath.Dir(filepath.Dir(pointerPath))
	dbPath, err := projresolve.ExpandPath(ptr.DBPath, root)
	if err != nil {
		return wire.ModeBExport{}, err
	}
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(root, dbPath)
	}
	if _, statErr := os.Stat(dbPath); statErr != nil {
		return wire.ModeBExport{}, errcodes.New(errcodes.NotFound, "migrate: the shared lore.db "+dbPath+" named by "+pointerPath+" is not there").WithCause(statErr)
	}
	db := dbent.InitDB(dbPath)
	defer joinClose(&err, db, "the DB")
	out := wire.ModeBExport{DBPath: dbPath, ProjectID: ptr.ProjectID}
	if scanErr := db.QueryRowContext(ctx, `SELECT name FROM `+projectsTable+` WHERE id = ?`, ptr.ProjectID).Scan(&out.ProjectName); scanErr != nil {
		return wire.ModeBExport{}, errcodes.New(errcodes.NotFound, "migrate: project "+ptr.ProjectID+" is not in "+dbPath).WithCause(scanErr)
	}
	ids, err := modeBRowIDs(ctx, db, ptr.ProjectID)
	if err != nil {
		return wire.ModeBExport{}, err
	}
	exported := map[string]bool{}
	for _, tr := range ids {
		b, ok, rowErr := lsync.RowDoc(ctx, db, tr.table, tr.id)
		if rowErr != nil {
			return wire.ModeBExport{}, errcodes.New(errcodes.Internal, "migrate: read "+tr.table+"/"+tr.id).WithCause(rowErr)
		}
		if ok {
			out.Docs = append(out.Docs, b)
			exported[tr.id] = true
		}
	}
	if out.TagLinks, err = modeBTagLinks(ctx, db, exported); err != nil {
		return wire.ModeBExport{}, err
	}
	return out, nil
}

// readModeBPointer parses lore.yaml or lore.toml without rewriting it.
func readModeBPointer(path string) (projresolve.Pointer, error) {
	var p projresolve.Pointer
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return p, errcodes.New(errcodes.Internal, "migrate: read "+path).WithCause(err)
	}
	if strings.HasSuffix(path, ".toml") {
		err = toml.Unmarshal(b, &p)
	} else {
		err = yaml.Unmarshal(b, &p)
	}
	if err == nil {
		err = p.Validate()
	}
	if err != nil {
		return p, errcodes.New(errcodes.InvalidInput, "migrate: "+path+" is not a Mode B pointer").WithCause(err)
	}
	return p, nil
}

// tableRow names one v0.1.17 row.
type tableRow struct{ table, id string }

// modeBRowIDs lists the project's rows in the core-owned synced tables: a
// table with a project column is filtered on it; a table without one
// (rule_verifier_refs, knowledge_refs…) keeps the rows whose *_id columns
// point at a row already chosen.
func modeBRowIDs(ctx context.Context, db *sql.DB, projectID string) ([]tableRow, error) {
	reg, _ := lsync.NewRegistry()
	var out []tableRow
	chosen := map[string]bool{}
	var orphans []string
	for _, t := range reg.Names() {
		if !lsync.CoreOwned(t) {
			continue
		}
		cols, err := tableColumns(ctx, db, t)
		if err != nil {
			return nil, err
		}
		if len(cols) == 0 {
			continue // not in this lore.db (an older schema)
		}
		col := ""
		for _, c := range []string{colProjectID, colEntityProjectID} {
			if slices.Contains(cols, c) {
				col = c
				break
			}
		}
		if col == "" {
			orphans = append(orphans, t)
			continue
		}
		got, err := queryIDs(ctx, db, `SELECT id FROM "`+t+`" WHERE "`+col+`" = ? ORDER BY id`, projectID)
		if err != nil {
			return nil, err
		}
		for _, id := range got {
			out = append(out, tableRow{t, id})
			chosen[id] = true
		}
	}
	for _, t := range orphans {
		cols, err := tableColumns(ctx, db, t)
		if err != nil {
			return nil, err
		}
		var refs []string
		for _, c := range cols {
			if c != colID && strings.HasSuffix(c, idColSuffix) {
				refs = append(refs, sqlIdent(c))
			}
		}
		if len(refs) == 0 {
			continue
		}
		rows, err := db.QueryContext(ctx, selectRefsQuery(t, refs))
		if err != nil {
			return nil, errcodes.New(errcodes.Internal, "migrate: list "+t).WithCause(err)
		}
		vals := make([]sql.NullString, len(refs)+1)
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				_ = rows.Close()
				return nil, errcodes.New(errcodes.Internal, "migrate: list "+t).WithCause(err)
			}
			for _, v := range vals[1:] {
				if v.Valid && chosen[v.String] {
					out = append(out, tableRow{t, vals[0].String})
					break
				}
			}
		}
		if err := rows.Close(); err != nil {
			return nil, errcodes.New(errcodes.Internal, "migrate: list "+t).WithCause(err)
		}
	}
	return out, nil
}

// modeBTagLinks reads the entity_tags rows of the exported rows.
func modeBTagLinks(ctx context.Context, db *sql.DB, exported map[string]bool) (_ []opsmemory.TagLink, err error) {
	cols, err := tableColumns(ctx, db, entityTagsTable)
	if err != nil || len(cols) == 0 {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT entity_table, entity_id, tag_id FROM `+entityTagsTable+` ORDER BY entity_id, tag_id`)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "migrate: list entity_tags").WithCause(err)
	}
	defer joinClose(&err, rows, "the query rows")
	var out []opsmemory.TagLink
	for rows.Next() {
		var l opsmemory.TagLink
		if err := rows.Scan(&l.EntityTable, &l.EntityID, &l.TagID); err != nil {
			return nil, errcodes.New(errcodes.Internal, "migrate: list entity_tags").WithCause(err)
		}
		if exported[l.EntityID] {
			out = append(out, l)
		}
	}
	return out, rows.Err()
}

// tableColumns returns a table's column names (none when it is absent).
func tableColumns(ctx context.Context, db *sql.DB, table string) (_ []string, err error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "migrate: columns of "+table).WithCause(err)
	}
	defer joinClose(&err, rows, "the query rows")
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, errcodes.New(errcodes.Internal, "migrate: columns of "+table).WithCause(err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// queryIDs runs a one-column id query.
func queryIDs(ctx context.Context, db *sql.DB, q string, args ...any) (_ []string, err error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "migrate: list ids").WithCause(err)
	}
	defer joinClose(&err, rows, "the query rows")
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, errcodes.New(errcodes.Internal, "migrate: list ids").WithCause(err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// sqlIdent quotes a SQLite identifier read from the database's own schema
// (a table or column name), doubling any embedded quote, so a name can never
// end the identifier early.
func sqlIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// selectRefsQuery is `SELECT id, <refs> FROM <table> ORDER BY id`; refs are
// already quoted with sqlIdent and table is quoted here.
func selectRefsQuery(table string, refs []string) string {
	var b strings.Builder
	b.WriteString("SELECT id, ")
	b.WriteString(strings.Join(refs, ", "))
	b.WriteString(" FROM ")
	b.WriteString(sqlIdent(table))
	b.WriteString(" ORDER BY id")
	return b.String()
}
