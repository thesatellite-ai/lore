package wire

// Retiring v0.1.17 Mode B (decision Q-MODEB).
//
// Mode B was a repo whose .lore/ held only a pointer file (.lore/lore.yaml,
// or the older .lore/lore.toml) naming a shared lore.db and a project_id in
// it. 0.1.18 replaces it with stores: the shared rows become a folder store
// attached through .lore/stores.yaml. This file holds the two pieces wire
// owns: the refusal every other command gives in such a repo, and the
// conversion `lore migrate run` performs. Reading the shared lore.db is the
// v0.1.17 code's job (its row codec), so it reaches wire through the
// ModeBExporter interface the binary supplies in Options.ModeB.

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Names of the Mode B conversion.
const (
	// ModeBMigratedSuffix is appended to the pointer file once the project
	// is converted: the user's config is kept, never deleted (owner).
	ModeBMigratedSuffix = ".migrated"
	// ModeBStoreName is the stores.yaml name the converted store gets.
	ModeBStoreName = "shared"
	// modeBStoresDir is the folder, beside the shared lore.db, that holds
	// one folder store per converted project (named by project name), so
	// every repo that pointed at the same project lands in the same store.
	modeBStoresDir = "stores"
	// modeBDir is the repo folder that holds the pointer file.
	modeBDir = ".lore"
	// modeBStepPrefix starts every step line the conversion reports.
	modeBStepPrefix = "mode-b: "
)

// modeBPointerNames are the v0.1.17 pointer files, newest first: lore.yaml
// (what v0.1.17 writes) and lore.toml (older; v0.1.17 read and converted it).
var modeBPointerNames = []string{"lore.yaml", "lore.toml"}

// ModeBExport is one Mode B project read out of its shared lore.db.
type ModeBExport struct {
	// DBPath is the shared lore.db, expanded (absolute).
	DBPath string
	// ProjectID is the project the pointer selected.
	ProjectID string
	// ProjectName is that project's name (it names the folder store).
	ProjectName string
	// Docs are the project's rows as v0.1.17 row documents (`_v: 1`, the
	// bytes a v0.1.17 export would write), only for CoreTables.
	Docs [][]byte
	// TagLinks are the project's v0.1.17 entity_tags rows; they become the
	// tag_ids of the rows they tag (decision O11).
	TagLinks []opsmemory.TagLink
}

// ModeBExporter reads a Mode B project out of its shared lore.db. The
// binary implements it with the v0.1.17 row codec; wire never opens the
// shared database itself.
type ModeBExporter interface {
	// ExportModeB parses the pointer file at pointerPath (lore.yaml or
	// lore.toml, without changing it) and returns the project it selects.
	ExportModeB(ctx context.Context, pointerPath string) (ModeBExport, error)
}

// FindModeBPointer returns the Mode B pointer file under repoTop/.lore
// (lore.yaml first, then lore.toml), ok=false when there is none.
func FindModeBPointer(repoTop string) (string, bool) {
	for _, n := range modeBPointerNames {
		p := filepath.Join(repoTop, modeBDir, n)
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			return p, true
		}
	}
	return "", false
}

// RetiredModeBMessage is the one sentence every command gives in a repo
// that still uses Mode B; pointer is the file found.
func RetiredModeBMessage(pointer string) string {
	return "this repo uses the retired shared-database mode (" + modeBDir + "/" + filepath.Base(pointer) + "); run `lore migrate run`"
}

// RetiredModeBHint is the hint beside RetiredModeBMessage.
const RetiredModeBHint = "`lore migrate run --dry-run` lists what moves: the project's rows become a folder store attached in .lore/stores.yaml, and the pointer is kept as <file>" + ModeBMigratedSuffix

// RefuseRetiredModeB returns E_UNSUPPORTED when the repo still has a Mode B
// pointer (Run calls it for every operation but migrate.run and the store
// operations).
func (a *App) RefuseRetiredModeB(ctx context.Context) error {
	top, err := a.repoTop(ctx)
	if err != nil {
		return err
	}
	if p, ok := FindModeBPointer(top); ok {
		return errs.New(errs.Unsupported, RetiredModeBMessage(p)).WithHint(RetiredModeBHint)
	}
	return nil
}

// MigrateModeB converts a Mode B repo (part of `lore migrate run`): the
// project's rows are written as `_v: 2` row files into a folder store
// beside the shared lore.db (<db dir>/stores/<project name>) through the
// folder backend, the store is added to .lore/stores.yaml as "shared", and
// the pointer file is renamed to <file>.migrated. It returns one step line
// per row and per change; under dryRun it changes nothing. A repo with no
// pointer returns nothing, so a second run is a no-op. A run cut short is
// resumed: rows already in the store are skipped, never overwritten. The
// int is the rows written into the store: every exported row under
// dryRun, the ones the store did not hold yet otherwise (a fresh run
// writes them all, so the two agree).
func (a *App) MigrateModeB(ctx context.Context, dryRun bool) ([]string, int, error) {
	top, err := a.repoTop(ctx)
	if err != nil {
		return nil, 0, err
	}
	pointer, ok := FindModeBPointer(top)
	if !ok {
		return nil, 0, nil
	}
	if a.opts.ModeB == nil {
		return nil, 0, errs.New(errs.Unsupported, "migrate: "+RetiredModeBMessage(pointer)+", but this build cannot read the shared lore.db")
	}
	exp, err := a.opts.ModeB.ExportModeB(ctx, pointer)
	if err != nil {
		return nil, 0, err
	}
	changes, err := modeBChanges(exp)
	if err != nil {
		return nil, 0, err
	}
	dir := filepath.Join(filepath.Dir(exp.DBPath), modeBStoresDir, exp.ProjectName)
	raw := (&url.URL{Scheme: folder.Scheme, Path: filepath.ToSlash(dir)}).String()
	steps := make([]string, 0, len(changes)+3)
	for _, c := range changes {
		steps = append(steps, modeBStepPrefix+c.Row.Table.String()+"/"+c.Row.ID.String()+" → store "+ModeBStoreName+" ("+dir+")")
	}
	steps = append(steps,
		modeBStepPrefix+attach.FileName+": add store "+ModeBStoreName+" "+raw,
		modeBStepPrefix+modeBDir+"/"+filepath.Base(pointer)+" → "+modeBDir+"/"+filepath.Base(pointer)+ModeBMigratedSuffix+" (kept)")
	if dryRun {
		return steps, len(changes), nil
	}
	written, err := a.writeModeBStore(ctx, raw, changes)
	if err != nil {
		return nil, 0, err
	}
	if err := a.attachModeBStore(ctx, top, raw); err != nil {
		return nil, 0, err
	}
	dst := pointer + ModeBMigratedSuffix
	if _, err := os.Lstat(dst); err == nil {
		return nil, 0, errs.New(errs.AlreadyInitialized, "migrate: "+dst+" already exists; the pointer "+pointer+" was left in place").
			WithHint("move one of the two files aside, then run `lore migrate run` again")
	}
	if err := os.Rename(pointer, dst); err != nil {
		return nil, 0, errs.Wrap(errs.Internal, "migrate: keep the pointer as "+dst, err)
	}
	return steps, written, nil
}

// modeBChanges decodes the exported v0.1.17 documents (model/row upcasts
// them), applies the per-table format steps of RewriteV1 and the tag links,
// and encodes each as a `_v: 2` row (store.FromRow). Every change expects
// the row to be absent.
func modeBChanges(exp ModeBExport) ([]store.Change, error) {
	reg, err := builtinRegistry()
	if err != nil {
		return nil, err
	}
	rows := make(map[taggedRow]*row.Row, len(exp.Docs))
	order := make([]taggedRow, 0, len(exp.Docs))
	for _, doc := range exp.Docs {
		r, err := row.Decode(doc)
		if err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "migrate: a row of the shared lore.db", err)
		}
		if step, ok := formatSteps[r.Table]; ok {
			r.Fields = step(r.Fields)
		}
		k := taggedRow{table: r.Table, id: r.ID}
		if _, dup := rows[k]; !dup {
			order = append(order, k)
		}
		rows[k] = &r
	}
	merged, _ := mergeTagLinks(reg, exp.TagLinks, func(t row.Table, id ids.AnyID) []string {
		r, ok := rows[taggedRow{table: t, id: id}]
		if !ok {
			return nil
		}
		// any form: a List, or a JSON array as a row file may decode
		return value.ItemStrings(r.Fields[schema.FieldTagIDs])
	})
	for k, tags := range merged {
		r, ok := rows[k]
		if !ok {
			continue // a link to a row that was not exported (another table): dropped with it
		}
		items := make([]value.Value, 0, len(tags))
		for _, t := range tags {
			tid, err := ids.ParseAny(t)
			if err != nil {
				return nil, err
			}
			ref, err := value.RefOf(tid)
			if err != nil {
				return nil, err
			}
			items = append(items, ref)
		}
		list, err := value.ListOf(value.KindRef, items...)
		if err != nil {
			return nil, err
		}
		if r.Fields == nil {
			r.Fields = map[string]value.Value{}
		}
		r.Fields[schema.FieldTagIDs] = list
	}
	slices.SortFunc(order, func(x, y taggedRow) int {
		if c := strings.Compare(x.table.String(), y.table.String()); c != 0 {
			return c
		}
		return strings.Compare(x.id.String(), y.id.String())
	})
	changes := make([]store.Change, 0, len(order))
	for _, k := range order {
		// audit_fields (FORMAT_EVOLUTION step 9) on the export itself, as
		// the v1 rewrite does (rewriteStore): a v0.1.17 row has no *_by
		// actors, and a row written without them made the next run rewrite
		// it (a second or resumed run must change nothing).
		filled, _ := auditfill.Defaults(*rows[k])
		up, err := store.FromRow(filled)
		if err != nil {
			return nil, err
		}
		changes = append(changes, store.Change{Row: up})
	}
	return changes, nil
}

// writeModeBStore makes the folder store at raw (a v2 marker, as `lore
// store init` writes) unless it exists, and writes the rows it does not
// hold yet, returning how many it wrote.
func (a *App) writeModeBStore(ctx context.Context, raw string, changes []store.Change) (int, error) {
	canon, err := canonical(attach.Store{URL: raw})
	if err != nil {
		return 0, err
	}
	id, err := ids.New[ids.Store]()
	if err != nil {
		return 0, errs.Wrap(errs.Internal, "migrate: new store id", err)
	}
	m := rowfs.Marker{ID: id, Visibility: rowfs.VisibilityInternal, Created: a.opts.Wall().UTC().Format(markerDateLayout), OrgFolder: defaultOrgFolder}
	if _, _, err = a.makeStore(ctx, canon, raw, m); err != nil {
		return 0, err
	}
	b, err := a.openStore(ctx, canon, raw)
	if err != nil {
		return 0, err
	}
	have, _, err := b.Read(ctx, "")
	if err != nil {
		return 0, errors.Join(err, b.Close())
	}
	held := make(map[store.Key]bool, len(have))
	for _, sr := range have {
		held[sr.Key()] = true
	}
	todo := make([]store.Change, 0, len(changes))
	for _, c := range changes {
		if !held[c.Row.Key()] {
			todo = append(todo, c)
		}
	}
	var werr error
	if len(todo) > 0 {
		werr = b.Write(ctx, todo)
	}
	if err := errors.Join(werr, b.Close()); err != nil {
		return 0, errs.Wrap(errs.CodeOf(err), "migrate: write the shared store", err)
	}
	return len(todo), nil
}

// attachModeBStore adds the store to .lore/stores.yaml unless an entry of
// that name already names the same url (a resumed run).
func (a *App) attachModeBStore(ctx context.Context, top, raw string) error {
	cfg, err := readStoresFile(top)
	if err != nil {
		return err
	}
	for _, s := range cfg.Stores {
		if string(s.Name) == ModeBStoreName && s.URL == raw {
			return nil
		}
	}
	if err := a.checkUnused(ctx, ModeBStoreName); err != nil {
		return errs.Wrap(errs.CodeOf(err), "migrate: name the converted store", err).
			WithHint("detach or rename the store named " + ModeBStoreName + " in " + attach.FileName + ", then run `lore migrate run` again")
	}
	return addStoresEntry(top, attach.Store{Name: store.Source(ModeBStoreName), URL: raw})
}
