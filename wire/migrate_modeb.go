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
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"

	"gopkg.in/yaml.v3"
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
	// ProjectName is that project's name (it names the folder store and
	// the project folder inside it, modeBFolder).
	ProjectName string
	// Project is the project's own row as a v0.1.17 document: it becomes
	// the project row of that folder, keeping its id (devco D-855).
	Project []byte
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
	return "this repo uses the retired shared-database mode (" + modeBDir + "/" + filepath.Base(pointer) + "); run lore fix"
}

// RetiredModeBHint is the hint beside RetiredModeBMessage.
const RetiredModeBHint = "`lore fix --dry-run` lists what moves: the project's rows become a folder store attached in .lore/stores.yaml, and the pointer is kept as <file>" + ModeBMigratedSuffix

// RefuseRetiredModeB returns E_UNSUPPORTED when the repo still has a Mode B
// pointer (Run calls it for every operation but migrate.run and the store
// operations).
func (a *App) RefuseRetiredModeB(ctx context.Context) error {
	if inStore(a.opts.RepoDir) {
		return nil // a store's checkout has no Mode B pointer of a repo (storehere.go)
	}
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
// project's rows, its own project row included, are written as `_v: 2` row
// files into the project folder (modeBFolder of the project name) of a
// folder store beside the shared lore.db (<db dir>/stores/<project name>)
// through the folder backend; the folder keeps the project's id
// (rowfs SeedProjectID). The store is added to .lore/stores.yaml as
// "shared" attached at that folder, and the pointer file is renamed to
// <file>.migrated: the kept pointer names the project, and the folder
// entry is where its rows live, so the repo is linked to the folder with
// nothing written into its tracked files beyond what the pointer already
// was (devco D-855: the rows used to land in the store's root folder with
// no project row, and every plain add and render then failed E_NOT_FOUND
// "this repository has no lore project"). It returns one step line per
// row and per change; under dryRun it changes nothing. A repo with no
// pointer is healed when the conversion of an older lore left it in that
// shape (healModeB), else returns nothing, so a second run is a no-op. A
// run cut short is resumed: rows already in the folder are skipped, never
// overwritten. The int is the rows written into the store: every exported
// row under dryRun, the ones the folder did not hold yet otherwise (a
// fresh run writes them all, so the two agree).
func (a *App) MigrateModeB(ctx context.Context, dryRun bool) ([]string, int, error) {
	top, err := a.repoTop(ctx)
	if err != nil {
		return nil, 0, err
	}
	pointer, ok := FindModeBPointer(top)
	if !ok {
		return a.healModeB(ctx, top, dryRun)
	}
	if a.opts.ModeB == nil {
		return nil, 0, errs.New(errs.Unsupported, "migrate: "+RetiredModeBMessage(pointer)+", but this build cannot read the shared lore.db")
	}
	exp, err := a.opts.ModeB.ExportModeB(ctx, pointer)
	if err != nil {
		return nil, 0, err
	}
	changes, err := modeBChanges(ctx, exp)
	if err != nil {
		return nil, 0, err
	}
	pid, err := ids.Parse[ids.Project](exp.ProjectID)
	if err != nil {
		return nil, 0, errs.Prefix("migrate: the project id of "+pointer, err)
	}
	dir := filepath.Join(filepath.Dir(exp.DBPath), modeBStoresDir, exp.ProjectName)
	raw := fileuri.URL(dir, "")
	folderName := modeBFolder(exp.ProjectName)
	steps := make([]string, 0, len(changes)+3)
	for _, c := range changes {
		steps = append(steps, modeBStepPrefix+c.Row.Table.String()+"/"+c.Row.ID.String()+" → "+modeBPlace(folderName)+" ("+dir+")")
	}
	steps = append(steps,
		modeBStepPrefix+attach.FileName+": add store "+ModeBStoreName+" "+raw+" at its folder "+folderName,
		modeBStepPrefix+modeBDir+"/"+filepath.Base(pointer)+" → "+modeBDir+"/"+filepath.Base(pointer)+ModeBMigratedSuffix+" (kept)")
	if dryRun {
		return steps, len(changes), nil
	}
	if err := a.markModeBWriting(ctx); err != nil {
		return nil, 0, err
	}
	written, err := a.writeModeBStore(ctx, dir, raw, folderName, pid, changes)
	if err != nil {
		return nil, 0, err
	}
	if err := a.attachModeBStore(ctx, top, raw, folderName); err != nil {
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

// modeBPlace is how a step line names the converted project's folder.
func modeBPlace(folderName string) string {
	return "store " + ModeBStoreName + ", folder " + folderName
}

// modeBFolderFallback is the folder of a project whose name gives no
// usable folder name (all punctuation, say).
const modeBFolderFallback = "project"

// modeBFolder is the store folder a converted project's rows go to: its
// v0.1.17 name, lower case, every character a folder name cannot hold
// turned into "-", and "-notes" added to a name lore reserves for a table
// (rowfs.CleanFolder's rules). Why a folder and not the store's root: a
// folder is a project (its _meta.json holds the prj_ id), the root is
// store-wide and belongs to no project.
func modeBFolder(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case b.Len() > 0:
			b.WriteByte('-')
		}
	}
	out := strings.TrimRight(b.String(), "-_")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	if out == "" {
		return modeBFolderFallback
	}
	if rowfs.IsTableDir(out) {
		out += "-notes"
	}
	if _, err := rowfs.CleanFolder(out); err != nil {
		return modeBFolderFallback
	}
	return out
}

// modeBChanges decodes the exported v0.1.17 documents (model/row upcasts
// them), applies the per-table format steps of RewriteV1 and the tag links,
// and encodes each as a `_v: 2` row (store.FromRow). Every change expects
// the row to be absent.
func modeBChanges(ctx context.Context, exp ModeBExport) ([]store.Change, error) {
	reg, err := builtinRegistry()
	if err != nil {
		return nil, err
	}
	docs := exp.Docs
	if len(exp.Project) > 0 {
		docs = append([][]byte{exp.Project}, docs...) // the folder's own project row (D-855)
	}
	rows := make(map[taggedRow]*row.Row, len(docs))
	order := make([]taggedRow, 0, len(docs))
	for _, doc := range docs {
		r, err := row.Decode(doc)
		if err != nil {
			return nil, errs.Prefix("migrate: a row of the shared lore.db", err)
		}
		if step, ok := formatSteps[r.Table]; ok {
			r.Fields = step(r.Fields)
		}
		r, _ = engine.NullEmptyRefs(reg, r) // v0.1.17's "" for "no ref" is written as null (engine/emptyref.go)
		k := taggedRow{table: r.Table, id: r.ID}
		if _, dup := rows[k]; !dup {
			order = append(order, k)
		}
		rows[k] = &r
	}
	// a ref holding a name, not an id (D-766): its row's id, or cleared
	all := make([]row.Row, 0, len(order))
	for _, k := range order {
		all = append(all, *rows[k])
	}
	names := engine.NewNameRefIndex(all)
	for _, k := range order {
		r := resolveNameRefs(ctx, reg, names, *rows[k])
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

// writeModeBStore makes the folder store at raw (dir on disk; a v2
// marker, as `lore store init` writes) unless it exists, gives its folder
// folderName the project id pid, and writes the rows that folder does not
// hold yet, returning how many it wrote.
func (a *App) writeModeBStore(ctx context.Context, dir, raw, folderName string, pid ids.ID[ids.Project], changes []store.Change) (int, error) {
	canon, err := canonical(attach.Store{URL: raw})
	if err != nil {
		return 0, err
	}
	id, err := ids.New[ids.Store]()
	if err != nil {
		return 0, errs.Wrap(errs.Internal, "migrate: new store id", err)
	}
	m := rowfs.Marker{ID: id, Visibility: rowfs.VisibilityInternal, Created: a.opts.Wall().UTC().Format(markerDateLayout)}
	if _, _, err = a.makeStore(ctx, canon, raw, m); err != nil {
		return 0, err
	}
	if err := seedModeBFolder(ctx, dir, folderName, pid); err != nil {
		return 0, err
	}
	fcanon, err := canonical(attach.Store{URL: raw, Folder: folderName})
	if err != nil {
		return 0, err
	}
	b, err := a.openStore(ctx, fcanon, raw)
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
		return 0, errs.Prefix("migrate: write the shared store", err)
	}
	return len(todo), nil
}

// seedModeBFolder gives the folder folderName of the folder store at dir
// the converted project's id before its first write (rowfs SeedProjectID).
func seedModeBFolder(ctx context.Context, dir, folderName string, pid ids.ID[ids.Project]) error {
	root, err := rowfs.Open(dir)
	if err != nil {
		return errs.Prefix("migrate: open the shared store", err)
	}
	f, err := root.Folder(folderName)
	if err != nil {
		return err
	}
	if err := f.SeedProjectID(ctx, pid); err != nil {
		return errs.Prefix("migrate: the project folder "+folderName+" of the shared store", err).
			WithHint("the folder belongs to another project; move it aside in " + dir + ", then run `lore fix` again")
	}
	return nil
}

// attachModeBStore adds the store, attached at folderName, to
// .lore/stores.yaml unless an entry of that name already names the same
// url and folder (a resumed run). An entry of that name and url attached
// whole (what an older lore's conversion wrote) gets the folder.
func (a *App) attachModeBStore(ctx context.Context, top, raw, folderName string) error {
	cfg, err := readStoresFile(top)
	if err != nil {
		return err
	}
	for _, s := range cfg.Stores {
		if string(s.Name) != ModeBStoreName || s.URL != raw {
			continue
		}
		if s.Folder == folderName {
			return nil
		}
		return setStoresEntryFolder(top, s.Name, folderName)
	}
	if err := a.checkUnused(ctx, ModeBStoreName, attach.Store{URL: raw, Folder: folderName}); err != nil {
		return errs.Prefix("migrate: name the converted store", err).
			WithHint("detach or rename the store named " + ModeBStoreName + " in " + attach.FileName + ", then run `lore fix` again")
	}
	return addStoresEntry(top, attach.Store{Name: store.Source(ModeBStoreName), URL: raw, Folder: folderName})
}

// setStoresEntryFolder sets the folder of the stores.yaml entry name,
// keeping the rest of the file as it is.
func setStoresEntryFolder(top string, name store.Source, folderName string) error {
	return editStoresFile(top, func(list *yaml.Node) error {
		for _, n := range list.Content {
			if v := mapGet(n, keyName); v != nil && v.Value == string(name) {
				mapSet(n, keyFolder, scalar(folderName))
				return nil
			}
		}
		return errs.New(errs.NotFound, "wire: no store "+string(name)+" in "+attach.FileName)
	})
}
