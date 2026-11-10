package wire

// v017edits.go — edits lore 0.1.17 kept only in a checkout's .lore/lore.db.
//
//	0.1.17 on a machine beside migrated (_v: 2) row files:
//	  `memory edit` ─▶ lore.db updated, "✓ updated"
//	                 ─▶ the _v: 2 file is refused ("newer-format"): not rewritten
//	the person upgrades that machine ─▶ the new lore reads the files only
//	                                  ─▶ the edit was gone without a word
//
// Found by the upgrader journey (week2/laptop-upgrades). Now:
//
//	lore doctor       ─▶ old_db_edits warning naming every such row
//	lore migrate run  ─▶ step old_db_edits: each one written into its row
//	                     (the lore.db values of the fields that differ)
//
// A row counts when lore.db holds it with an updated_at later than the
// stored row's (a row file without one has nothing to compare) and a field
// the row file carries differs; only those fields are written. Rows lore.db holds that no
// store has are left alone: a teammate's delete looks the same. lore.db is
// opened read-only (0.1.17 may still use it) and never changed.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// DoctorCheckOldDBEdits names the doctor check.
const DoctorCheckOldDBEdits DoctorCheck = "old_db_edits"

// StepOldDBEdits names the migrate step (an untyped string, as the other
// migrate step names).
const StepOldDBEdits = "old_db_edits"

const (
	oldDBEditsWarn = "%s saved by lore 0.1.17 only in " + v017LoreDB + " (it could not write the migrated row files): %s; no file holds them, so no other machine or command sees them"
	oldDBEditsFix  = "lore migrate run (writes them into their rows)"
	// v017UpdatedAt is the update time column of every v0.1.17 ent table.
	v017UpdatedAt = "updated_at"
)

// v017Edit is one row lore.db holds newer than its store.
type v017Edit struct {
	ref   engine.Ref
	patch map[string]value.Value // the lore.db values of the fields that differ
}

func (e v017Edit) name() string { return string(e.ref.Table) + "/" + e.ref.ID.String() }

// v017Edits lists the rows of the checkout's lore.db newer than their
// stored copy (none without lore.db).
func (a *App) v017Edits(ctx context.Context) ([]v017Edit, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(st.repoTop, filepath.FromSlash(v017LoreDB))
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, errs.Wrap(errs.Internal, "stat "+v017LoreDB, err)
	}
	db, err := sql.Open(sqliteDriver, fileuri.URL(path, "mode=ro"))
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "open "+v017LoreDB, err)
	}
	defer func() { _ = db.Close() }() // read-only handle
	var out []v017Edit
	for _, def := range st.eng.Registry().Tables() {
		if def.MachineLocal {
			continue // copied by the machine_local step
		}
		cols, err := v017Columns(ctx, db, string(def.Name))
		if err != nil {
			return nil, err
		}
		if len(cols) == 0 || !cols[v017UpdatedAt] {
			continue
		}
		q, fields, err := v017SelectRows(def, cols)
		if err != nil {
			return nil, err
		}
		q = strings.Replace(q, "SELECT json_object(", `SELECT json_object('`+v017UpdatedAt+`', "`+v017UpdatedAt+`", `, 1)
		edits, err := v017TableEdits(ctx, st.eng, db, q, def.Name, fields)
		if err != nil {
			return nil, err
		}
		out = append(out, edits...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name() < out[j].name() })
	return out, nil
}

// v017TableEdits reads one table of lore.db (q from v017SelectRows plus
// updated_at) and keeps the rows newer than their stored copy.
func v017TableEdits(ctx context.Context, eng *engine.Engine, db *sql.DB, q string, table row.Table, fields []schema.FieldDef) ([]v017Edit, error) {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "read "+v017LoreDB, err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	var out []v017Edit
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, errs.Wrap(errs.Internal, "read "+v017LoreDB, err)
		}
		var cells map[string]json.RawMessage
		if err := json.Unmarshal([]byte(doc), &cells); err != nil {
			return nil, errs.Wrap(errs.Internal, "read "+v017LoreDB, err)
		}
		var rawID, rawAt string
		if json.Unmarshal(cells[v017IDColumn], &rawID) != nil || json.Unmarshal(cells[v017UpdatedAt], &rawAt) != nil {
			continue // no id or no update time: nothing to compare
		}
		id, perr := ids.ParseAny(rawID)
		at, terr := v017Time(rawAt)
		if perr != nil || terr != nil {
			continue
		}
		ref := engine.Ref{Table: table, ID: id}
		_, cur, gerr := eng.Get(ctx, ref)
		if errs.Has(gerr, errs.NotFound) || errs.Has(gerr, errs.Purged) {
			continue // see the file comment: a delete looks the same
		} else if gerr != nil {
			return nil, gerr
		}
		dbAt, _ := at.(value.DateTime)
		if cur.Deleted || cur.UpdatedAt.IsZero() || !dbAt.Time().Truncate(time.Millisecond).After(cur.UpdatedAt.Truncate(time.Millisecond)) {
			continue
		}
		patch := map[string]value.Value{}
		for _, f := range fields {
			v, set, verr := v017Value(f, cells[f.Name])
			if verr != nil || !set {
				continue
			}
			// Only a field the row file carries: 0.1.17 leaves some out of
			// its files on purpose (an actor's stable_key is an email,
			// last_seen_at is per machine), and those must stay out.
			if old, ok := cur.Fields[f.Name]; !ok || v017Same(old, v) {
				continue
			}
			if table == schema.TableActors && actorCopiedField(f.Name) {
				continue // a copy of the actor's source (actorCopiedField)
			}
			patch[f.Name] = v
		}
		if len(patch) > 0 {
			out = append(out, v017Edit{ref: ref, patch: patch})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(errs.Internal, "read "+v017LoreDB, err)
	}
	return out, nil
}

// actorCopiedField reports whether name is an actors field every store's
// copy repeats from the actor's source of truth (the identity, or the org
// roster row: actor_source.go). lore.db's value of such a field is 0.1.17's
// own copy, never the source: when the source changed (a rename, or the git
// fallback's display name) and lore.db's row only looks newer than the
// refreshed file (another machine's clock), writing its older name back
// would undo the source's change. The source wins (SOURCE_OF_TRUTH).
func actorCopiedField(name string) bool {
	switch name {
	case actorFieldDisplay, actorFieldKind, actorFieldKey, actorFieldHandle:
		return true
	}
	return false
}

// v017EditNames is the rows as "table/id, …".
func v017EditNames(edits []v017Edit) string {
	names := make([]string, len(edits))
	for i, e := range edits {
		names[i] = e.name()
	}
	return strings.Join(names, ", ")
}

// DoctorOldDBEdits warns about the edits only lore.db holds.
func (a *App) DoctorOldDBEdits(ctx context.Context) ([]DoctorFinding, error) {
	edits, err := a.v017Edits(ctx)
	if err != nil || len(edits) == 0 {
		return nil, err
	}
	return []DoctorFinding{{Check: DoctorCheckOldDBEdits, Level: DoctorWarning,
		Message: fmt.Sprintf(oldDBEditsWarn, plural(len(edits), "edit"), v017EditNames(edits)),
		Fix:     oldDBEditsFix}}, nil
}

// migrateOldDBEdits is the migrate step: each edit written into its row.
func (a *App) migrateOldDBEdits(ctx context.Context, dryRun bool) ([]string, int, error) {
	edits, err := a.v017Edits(ctx)
	if err != nil || len(edits) == 0 {
		return nil, 0, err
	}
	verb := "brought in"
	if dryRun {
		verb = "would bring in"
	}
	if !dryRun {
		st, err := a.state(ctx)
		if err != nil {
			return nil, 0, err
		}
		for _, e := range edits {
			if _, err := st.eng.Update(ctx, e.ref, e.patch); err != nil {
				return nil, 0, errs.Prefix("migrate: "+StepOldDBEdits+" "+e.name(), err)
			}
		}
		a.wrote(nil)
	}
	return []string{StepOldDBEdits + ": " + verb + " " + plural(len(edits), "edit") + " lore 0.1.17 kept only in " + v017LoreDB + ": " + v017EditNames(edits)}, len(edits), nil
}

// v017Same reports whether a stored field and a lore.db cell hold the same
// value. A decoded row file types some fields loosely (an id or a time can
// read back as text), so the printed forms are compared too.
func v017Same(stored, cell value.Value) bool {
	return value.Equal(stored, cell) || fmt.Sprint(stored) == fmt.Sprint(cell)
}
