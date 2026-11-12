package wire

// storeadopt.go — opsstores.Adopter, `lore store adopt <store>/<folder>
// [--from <repo>] [--remove-source]` (ops/stores/adopt.go has the flow, the
// question and why it is not `lore move --all`).
//
//	adoptSource: the repo's rows as migrate would make them, without
//	   writing the repo
//	     .lore/data (any format; v1 upcast, format steps, audit fields,
//	        v0.1.17 entity_tags folded into tag_ids)
//	   + rows only on the v0.1.17 live ref (merged newer-wins with the
//	        branch copy, LiveStamp for a bare "agent" writer)
//	   + rows only in the repo's v0.1.17 lore.db (migrate_repodb.go)
//	adoptChanges: every row but the place rows (projects, repos), the
//	   folded entity_tags and machine-local tables; project_id moves to
//	   the folder's project; id, HLC, every other field and relation as
//	   they are
//	   vs the folder's rows: absent or older ─▶ written; the same bytes
//	   ─▶ unchanged; newer or deleted there ─▶ kept (never overwritten,
//	   never brought back)
//	write: one rowfs batch in the store's local copy, ONE commit (the org
//	   identity signs an org store's), pushed per the store's push mode
//	--remove-source: then the adopted branch row files are deleted from
//	   the repo's .lore/data (a diff the person commits)
//
// Why it is fast: two folder reads, one batch, one git add/commit, one
// push; no engine write per row (each was a commit and a cache refresh).

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

var _ opsstores.Adopter = (*App)(nil)

// Adoption wording.
const (
	adoptVerb    = "store adopt"
	adoptSubject = "lore: store adopt "
	// adoptProjectField is the field that places a row in a project.
	adoptProjectField = "project_id"
)

// adoptOrigin says where one adopted row came from.
type adoptOrigin int

const (
	fromBranch adoptOrigin = iota // a row file of .lore/data
	fromLive                      // the repo's live ref only (or newer there)
	fromLoreDB                    // the repo's v0.1.17 lore.db only
)

// adoptRow is one source row and where it came from.
type adoptRow struct {
	r    row.Row
	from adoptOrigin
	// file: the repo holds a row file of it (--remove-source deletes it),
	// also when a newer live copy was merged in.
	file bool
}

// adoptSourceRows is what adoptSource read: the rows, and the v0.1.17
// entity_tags row files by the id of the row each tags (folded into
// tag_ids, so --remove-source deletes them with that row).
type adoptSourceRows struct {
	rows  []adoptRow
	links map[string][]store.Key
}

// Adopt implements opsstores.Adopter.
func (a *App) Adopt(ctx context.Context, in opsstores.AdoptIn, name, folderName string) (opsstores.AdoptOut, error) {
	at, err := a.folderStore(ctx, adoptVerb, name, folderName) // the org's store that holds the folder (foldertarget.go)
	if err != nil {
		return opsstores.AdoptOut{}, err
	}
	name = at.Name
	src, err := a.findStore(ctx, adoptVerb, name)
	if err != nil {
		return opsstores.AdoptOut{}, err
	}
	label := at.Label
	top, err := a.adoptTop(ctx, in.From)
	if err != nil {
		return opsstores.AdoptOut{}, err
	}
	dir, err := a.storeDir(ctx, src)
	if err != nil {
		return opsstores.AdoptOut{}, errs.Prefix(adoptVerb+": store "+label, err)
	}
	out := opsstores.AdoptOut{Store: label, Folder: folderName, From: top, DryRun: in.DryRun}
	if a.isOrgFolder(src, folderName) {
		return opsstores.AdoptOut{}, errs.New(errs.Refused, adoptVerb+": "+folderName+" is the org folder of "+label+" (org_folder): it holds the org's roster, teams and lore-org.yaml, not a repo's notes").
			WithHint("adopt into a client or project folder: lore store folder add " + name + " <folder>")
	}
	meta := filepath.Join(dir, filepath.FromSlash(rowfs.LayoutStore.StorePath(folderName, rowfs.MetaFileName)))
	if _, serr := os.Stat(meta); errors.Is(serr, fs.ErrNotExist) {
		return opsstores.AdoptOut{}, errs.New(errs.NotFound, adoptVerb+": "+label+" has no folder "+folderName+a.folderMissDetail(ctx)+"; nothing written").
			WithHint("make it first: lore store folder add " + name + " " + folderName + " (`lore store folder list " + name + "` lists its folders)")
	}
	root, err := rowfs.Open(dir, a.opts.storeRowfsOptions(dir)...)
	if err != nil {
		return opsstores.AdoptOut{}, err
	}
	f, err := root.Folder(folderName)
	if err != nil {
		return opsstores.AdoptOut{}, err
	}
	pid, err := f.ProjectID(ctx)
	if err != nil {
		return opsstores.AdoptOut{}, err
	}
	out.ProjectID = pid.String()
	if gb, done, gerr := a.openGitStore(ctx, src); gerr == nil {
		place, _ := a.removePlace(src, name, gb.Dir())
		out.Push = string(a.storeSettings(gb, place).Push)
		done()
	}
	srcRows, left, err := a.adoptSource(ctx, top)
	if err != nil {
		return opsstores.AdoptOut{}, err
	}
	out.Left = left
	have, _, err := f.Read(ctx, "")
	if err != nil {
		return opsstores.AdoptOut{}, err
	}
	adoptRelink(srcRows.rows, &out) // --supersedes links of lore 0.1.16 and older (hostbase_doctor_supersede.go)
	rows, mapped, err := a.adoptActorMap(ctx, srcRows.rows, have, &out)
	if err != nil {
		return opsstores.AdoptOut{}, err
	}
	changes, adopted, err := adoptChanges(rows, have, pid, &out)
	if err != nil {
		return opsstores.AdoptOut{}, err
	}
	adopted = append(adopted, mapped...)
	for _, k := range slices.Clone(adopted) {
		adopted = append(adopted, srcRows.links[k.ID.String()]...)
	}
	if in.RemoveSource && out.Live > 0 {
		out.Left = append(out.Left, "refs/lore/live: "+plural(out.Live, "adopted row")+" stay on the live ref (--remove-source deletes row files only, never a git ref)")
	}
	if in.DryRun {
		if in.RemoveSource {
			out.Removed = countPresent(top, adopted) // what the run would delete
		}
		return out, nil
	}
	if len(changes) > 0 {
		subject := adoptSubject + folderName + " from " + filepath.Base(top) + " (" + plural(len(changes), "row") + ")"
		pushed, note, cerr := a.commitStoreEdit(ctx, src, name, subject, func(dir string) ([]string, error) {
			return adoptWrite(ctx, a.opts.storeRowfsOptions(dir), dir, folderName, changes)
		})
		if cerr != nil {
			return opsstores.AdoptOut{}, errs.Prefix(adoptVerb+": write "+label+"/"+folderName, cerr)
		}
		out.Pushed, out.Note = pushed, note
	}
	if in.RemoveSource {
		if out.Removed, err = removeAdopted(top, adopted); err != nil {
			return out, errs.Prefix(adoptVerb+": the rows are in "+label+"/"+folderName+" but the repo's copies could not all be removed", err).
				WithHint("rerun the same command: it writes nothing new and removes what is left")
		}
	}
	return out, nil
}

// adoptTop is the git top of the repo to adopt: from (relative to the
// folder the command runs in), else the repo the command runs in. It
// must hold lore data.
func (a *App) adoptTop(ctx context.Context, from string) (string, error) {
	dir := a.opts.RepoDir
	if from != "" {
		if !filepath.IsAbs(from) {
			from = filepath.Join(a.opts.RepoDir, from)
		}
		dir = filepath.Clean(from)
	}
	top, err := git.RepoRoot(ctx, dir, a.opts.Home)
	if err != nil {
		if from == "" {
			return "", errs.New(errs.NotProjectRoot, adoptVerb+": "+dir+" is not inside a git repo, so there is no repo to adopt").
				WithHint("run it in the repo, or name it: lore store adopt <store>/<folder> --from <repo path>")
		}
		return "", errs.Wrap(errs.NotProjectRoot, adoptVerb+": --from "+from+" is not a git repo", err).
			WithHint("name the repo's folder (a clone of it works the same)")
	}
	if fi, serr := os.Stat(filepath.Join(top, rowfs.LoreDir)); serr != nil || !fi.IsDir() {
		return "", errs.New(errs.NotProjectRoot, adoptVerb+": "+top+" has no lore data (no .lore folder); nothing to adopt").
			WithHint("name the repo that holds the notes with --from")
	}
	return top, nil
}

// adoptSource reads every row of the repo at top (see the file comment),
// keyed and sorted, and what is left out with why.
func (a *App) adoptSource(ctx context.Context, top string) (adoptSourceRows, []string, error) {
	rows, linkFiles, left, err := a.readAdoptSource(ctx, top)
	return adoptSourceRows{rows: rows, links: linkFiles}, left, err
}

// readAdoptSource is adoptSource's reader.
func (a *App) readAdoptSource(ctx context.Context, top string) ([]adoptRow, map[string][]store.Key, []string, error) {
	reg, err := builtinRegistry()
	if err != nil {
		return nil, nil, nil, err
	}
	root, err := rowfs.Open(top, a.opts.repoRowfsOptions()...)
	if err != nil {
		return nil, nil, nil, err
	}
	f, err := root.Folder(rowfs.FolderRoot)
	if err != nil {
		return nil, nil, nil, err
	}
	files, _, err := f.Read(ctx, "")
	if err != nil {
		return nil, nil, nil, err
	}
	var left []string
	if probs := f.ReadProblems(); len(probs) > 0 {
		left = append(left, plural(len(probs), "unreadable row file")+" (first: "+probs[0].Path+": "+probs[0].Reason+"; `lore doctor` there names each)")
	}
	byKey := map[store.Key]*adoptRow{}
	var links []opsmemory.TagLink
	linkFiles := map[string][]store.Key{}
	for _, sr := range files {
		if sr.Table.String() == entityTagsTable {
			if !sr.Deleted {
				if l, ok := v1TagLinkOf(sr.Doc); ok {
					links = append(links, l)
					linkFiles[l.EntityID] = append(linkFiles[l.EntityID], sr.Key())
				}
			}
			continue
		}
		r, derr := sr.Decode()
		if derr != nil {
			return nil, nil, nil, errs.Prefix(adoptVerb+": "+sr.Table.String()+"/"+sr.ID.String(), derr)
		}
		byKey[sr.Key()] = &adoptRow{r: r, from: fromBranch, file: true}
	}
	// the v0.1.17 live ref: rows only there, or newer there
	if folder, ferr := repoLiveFolder(ctx, top); ferr == nil {
		live, lerr := readLiveRows(ctx, top, []string{liveRefLocal, liveRefTracking}, folder, liveTables())
		if lerr != nil {
			return nil, nil, nil, lerr
		}
		for _, lr := range live {
			k := store.Key{Table: lr.row.Table, ID: lr.row.ID}
			cur, ok := byKey[k]
			switch {
			case !ok:
				byKey[k] = &adoptRow{r: lr.row, from: fromLive}
			case cur.r.Deleted || trashed(cur.r) || cur.r.HLC.Compare(lr.row.HLC) >= 0:
				// the row file is newer, deleted or trashed: it stands
			default:
				m, merr := mergeRow(cur.r, lr.row)
				if merr != nil {
					return nil, nil, nil, merr
				}
				m.SourceVersion = min(cur.r.SourceVersion, lr.row.SourceVersion)
				byKey[k] = &adoptRow{r: m, from: fromLive, file: true}
			}
		}
	}
	// the repo's v0.1.17 lore.db: rows neither the files nor the live ref
	// hold, while 0.1.18 has written nothing here (migrate_repodb.go)
	if exp, ok := a.opts.ModeB.(RepoDBExporter); ok && !anyCurrent(files) {
		if db := filepath.Join(top, filepath.FromSlash(v017LoreDB)); regularFile(db) {
			pid, perr := repoMetaProject(top)
			if perr != nil {
				return nil, nil, nil, perr
			}
			got, xerr := exp.ExportRepoDB(ctx, db, pid)
			if xerr != nil {
				return nil, nil, nil, xerr
			}
			changes, cerr := modeBChanges(got)
			if cerr != nil {
				return nil, nil, nil, errs.Prefix(adoptVerb+": "+v017LoreDB, cerr)
			}
			for _, c := range changes {
				if _, held := byKey[c.Row.Key()]; held {
					continue
				}
				r, derr := c.Row.Decode()
				if derr != nil {
					return nil, nil, nil, derr
				}
				byKey[c.Row.Key()] = &adoptRow{r: r, from: fromLoreDB}
			}
		}
	}
	// v0.1.17 tag links become the tag_ids of the rows they tag (O11)
	merged, skipped := mergeTagLinks(reg, links, func(t row.Table, id ids.AnyID) []string {
		if x, ok := byKey[store.Key{Table: t, ID: id}]; ok {
			return value.ItemStrings(x.r.Fields[schema.FieldTagIDs])
		}
		return nil
	})
	if len(skipped) > 0 {
		left = append(left, entityTagsTable+": "+plural(len(skipped), "link")+" that tag no row of a table with tag_ids")
	}
	for k, tags := range merged {
		x, ok := byKey[store.Key{Table: k.table, ID: k.id}]
		if !ok {
			continue
		}
		list, lerr := tagList(tags)
		if lerr != nil {
			return nil, nil, nil, lerr
		}
		if x.r.Fields == nil {
			x.r.Fields = map[string]value.Value{}
		}
		x.r.Fields[schema.FieldTagIDs] = list
	}
	keys := slices.SortedFunc(maps.Keys(byKey), func(x, y store.Key) int {
		if c := strings.Compare(x.Table.String(), y.Table.String()); c != 0 {
			return c
		}
		return strings.Compare(x.ID.String(), y.ID.String())
	})
	out := make([]adoptRow, 0, len(keys))
	for _, k := range keys {
		out = append(out, *byKey[k])
	}
	return out, linkFiles, left, nil
}

// trashed reports a row in the trash (deleted_at set).
func trashed(r row.Row) bool {
	at, ok := r.Fields[auditfill.FieldDeletedAt]
	return ok && at != nil && at.String() != ""
}

// regularFile reports whether path is a regular file (a link is not).
func regularFile(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode().IsRegular()
}

// tagList is a tag_ids value of tag ids.
func tagList(tags []string) (value.Value, error) {
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
	return value.ListOf(value.KindRef, items...)
}

// adoptSkipped are the tables a folder never takes from a repo, with why:
// the rows that name the place itself.
var adoptSkipped = map[row.Table]string{
	schema.TableProjects: "the folder is its own project",
	schema.TableRepos:    "the code repo's own row; a folder links repos with lore org link",
}

// adoptChanges turns the source rows into the folder's changes (see the
// file comment), filling out's counts; adopted lists the branch rows now
// in the folder (written, unchanged or kept), for --remove-source.
func adoptChanges(rows []adoptRow, have []store.Row, pid ids.ID[ids.Project], out *opsstores.AdoptOut) ([]store.Change, []store.Key, error) {
	reg, err := builtinRegistry()
	if err != nil {
		return nil, nil, err
	}
	there := make(map[store.Key]store.Row, len(have))
	for _, h := range have {
		there[h.Key()] = h
	}
	sourceProjects := map[string]bool{}
	for _, x := range rows {
		if x.r.Table == schema.TableProjects {
			sourceProjects[x.r.ID.String()] = true
		}
	}
	folderProject, err := value.RefOf(pid.Any())
	if err != nil {
		return nil, nil, err
	}
	leftCount := map[string]int{}
	perTable := map[string]int{}
	var changes []store.Change
	var adopted []store.Key
	for _, x := range rows {
		r := x.r
		def, ok := reg.Table(r.Table)
		switch {
		case !ok:
			leftCount[r.Table.String()+": no such table in this lore"]++
			continue
		case def.MachineLocal:
			leftCount[r.Table.String()+": this machine's own rows"]++
			continue
		}
		if why, skip := adoptSkipped[r.Table]; skip {
			leftCount[r.Table.String()+": "+why]++
			continue
		}
		if r.SourceVersion < row.FormatVersion {
			if step, ok := formatSteps[r.Table]; ok && r.Fields != nil {
				r.Fields = step(r.Fields)
			}
		}
		r, _ = engine.NullEmptyRefs(reg, r) // v0.1.17's "" for "no ref" is written as null (engine/emptyref.go)
		r, _ = auditfill.Defaults(r)        // audit_fields (step 9), as migrate writes them
		if f, has := def.Field(adoptProjectField); has && f.RefTable == schema.TableProjects && r.Fields != nil {
			if cur, set := r.Fields[adoptProjectField]; set && sourceProjects[cur.String()] {
				r.Fields = maps.Clone(r.Fields)
				r.Fields[adoptProjectField] = folderProject
			}
		}
		sr, err := store.FromRow(r)
		if err != nil {
			return nil, nil, errs.Prefix(adoptVerb+": "+r.Table.String()+"/"+r.ID.String(), err)
		}
		cur, held := there[sr.Key()]
		switch {
		case !held:
			changes = append(changes, store.Change{Row: sr})
		case string(cur.Doc) == string(sr.Doc):
			out.Unchanged++
		case cur.Deleted || cur.HLC.Compare(sr.HLC) >= 0:
			out.Kept++
		default:
			changes = append(changes, store.Change{Row: sr, Expect: cur.HLC})
		}
		if x.file {
			adopted = append(adopted, sr.Key())
		}
		if !held || (string(cur.Doc) != string(sr.Doc) && !cur.Deleted && cur.HLC.Compare(sr.HLC) < 0) {
			out.Written++
			perTable[r.Table.String()]++
			switch x.from {
			case fromLive:
				out.Live++
			case fromLoreDB:
				out.LoreDB++
			}
		}
	}
	for _, t := range slices.Sorted(maps.Keys(perTable)) {
		out.Tables = append(out.Tables, opsstores.AdoptTable{Table: t, Rows: perTable[t]})
	}
	for _, k := range slices.Sorted(maps.Keys(leftCount)) {
		t, why, _ := strings.Cut(k, ": ")
		out.Left = append(out.Left, t+": "+plural(leftCount[k], "row")+" ("+why+")")
	}
	return changes, adopted, nil
}

// adoptWrite applies the changes to the folder of the store's local copy
// at dir under the store lock, all or none, and returns the path to
// commit: the folder's data path (one argument however many rows).
func adoptWrite(ctx context.Context, opts []rowfs.Option, dir, folderName string, changes []store.Change) ([]string, error) {
	root, err := rowfs.Open(dir, opts...)
	if err != nil {
		return nil, err
	}
	lock, err := root.Lock(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Release() }() // released after the batch; a failed release only delays the next writer
	f, err := root.Folder(folderName)
	if err != nil {
		return nil, err
	}
	applied, err := f.Apply(ctx, changes)
	if err != nil {
		if errors.Is(err, store.ErrStale) {
			return nil, errs.Wrap(errs.Stale, "a row of the folder changed while it was being adopted; nothing written", err).
				WithHint("run the same command again")
		}
		return nil, err
	}
	return commitPaths(f, applied.Paths), nil
}

// commitPaths turns the files a batch wrote into the folder f into few git
// pathspecs: one per table folder that got rows (however many rows), and
// every other file (the folder's _meta.json, the store marker) as it is.
// A pathspec never names the folder itself: in a store that would also
// take in the folders below it and a person's files.
func commitPaths(f *rowfs.Folder, written []string) []string {
	var paths []string
	for _, p := range written {
		spec := p
		if rel, ok := f.FileRel(p); ok {
			table, _, _ := rowfs.ParseRowPath(rel)
			spec = f.Layout().StorePath(f.Name(), table.String())
		}
		if !slices.Contains(paths, spec) {
			paths = append(paths, spec)
		}
	}
	return paths
}

// countPresent counts the adopted branch row files still in the repo.
func countPresent(top string, adopted []store.Key) int {
	data := filepath.Join(top, filepath.FromSlash(rowfs.LayoutRepo.DataPath(rowfs.FolderRoot)))
	n := 0
	for _, k := range adopted {
		if regularFile(filepath.Join(data, filepath.FromSlash(rowfs.RowPath(k.Table, k.ID)))) {
			n++
		}
	}
	return n
}

// removeAdopted deletes the adopted branch row files from the repo at top
// (--remove-source) and returns how many it deleted. A file already gone
// counts nothing (a rerun finishes the job).
func removeAdopted(top string, adopted []store.Key) (int, error) {
	data := filepath.Join(top, filepath.FromSlash(rowfs.LayoutRepo.DataPath(rowfs.FolderRoot)))
	n := 0
	for _, k := range adopted {
		p := filepath.Join(data, filepath.FromSlash(rowfs.RowPath(k.Table, k.ID)))
		switch err := os.Remove(p); {
		case err == nil:
			n++
		case errors.Is(err, fs.ErrNotExist):
		default:
			return n, errs.Wrap(errs.Internal, "remove "+p, err)
		}
	}
	return n, nil
}

// adoptRelink moves the --supersedes links lore 0.1.16 and older wrote on
// the new row onto the old row in the rows being adopted (the doctor
// repair's plan, planSupersedeRepair), so adopted v0.1.17 data never
// shows as degraded. The same rows give the same rewrite, so a rerun is
// still byte-identical. Rows two links claim are named in out.Conflicts
// and left as they are.
func adoptRelink(rows []adoptRow, out *opsstores.AdoptOut) {
	for _, k := range supersedeKinds {
		var nodes []supersedeNode
		at := map[string]int{}
		for i, x := range rows {
			if x.r.Table != k.table || x.r.Deleted {
				continue
			}
			n := supersedeNode{id: x.r.ID.String(), projectID: fieldText(x.r, supersedeFieldProject), supersededBy: fieldText(x.r, supersedeFieldNext), createdAt: x.r.CreatedAt}
			if k.table == schema.TableDecisions {
				n.decisionStatus = fieldText(x.r, supersedeFieldStatus)
			}
			nodes = append(nodes, n)
			at[n.id] = i
		}
		p := planSupersedeRepair(k.name, nodes)
		for _, c := range p.conflicts {
			out.Conflicts = append(out.Conflicts, conflictText(c))
		}
		if len(p.fixes) == 0 {
			continue
		}
		for id, next := range p.set {
			i := at[id]
			r := rows[i].r
			r.Fields = maps.Clone(r.Fields)
			if r.Fields == nil {
				r.Fields = map[string]value.Value{}
			}
			r.Fields[supersedeFieldNext] = value.Null{}
			if next != "" {
				nid, err := ids.ParseAny(next)
				if err != nil {
					continue // an id that does not parse is no row of the plan
				}
				ref, err := value.RefOf(nid)
				if err != nil {
					continue
				}
				r.Fields[supersedeFieldNext] = ref
				if s := fieldText(r, supersedeFieldStatus); k.table == schema.TableDecisions && (s == schema.DecisionStatusProposed || s == schema.DecisionStatusAccepted) {
					if st, err := value.TextOf(schema.DecisionStatusSuperseded); err == nil {
						r.Fields[supersedeFieldStatus] = st
					}
				}
			}
			rows[i].r = r
		}
		out.Relinked += len(p.fixes)
	}
}

// adoptActorMap maps each source actor whose stable key the org roster
// or the folder already holds under another id onto that actor (owner
// report 2026-11-12: adopting a v0.1.17 repo brought in a second actor
// id for a person already on the roster). The mapped actor rows are not
// adopted (their row files, for --remove-source, are returned), and every
// other row names the existing actor instead; the same source gives the
// same rows, so a rerun stays a no-op. out.Actors names each mapping.
func (a *App) adoptActorMap(ctx context.Context, rows []adoptRow, have []store.Row, out *opsstores.AdoptOut) ([]adoptRow, []store.Key, error) {
	var src []row.Row
	for _, x := range rows {
		if x.r.Table == schema.TableActors {
			src = append(src, x.r)
		}
	}
	if len(src) == 0 {
		return rows, nil, nil
	}
	known, err := a.knownActorKeys(ctx)
	if err != nil {
		return nil, nil, errs.Prefix(adoptVerb, err).WithHint("an adopt maps the repo's actors onto the roster's; run it where the org roster is reachable (`lore sync`, then again)")
	}
	var folderActors []row.Row
	for _, h := range have {
		if h.Table != schema.TableActors {
			continue
		}
		r, derr := h.Decode()
		if derr != nil {
			continue // an unreadable row file: doctor's row_file check names it
		}
		folderActors = append(folderActors, r)
	}
	addActorKeys(known, folderActors)
	m, lines := actorKeyMap(src, known)
	if len(m) == 0 {
		return rows, nil, nil
	}
	out.Actors = lines
	kept := make([]adoptRow, 0, len(rows))
	var mapped []store.Key
	for _, x := range rows {
		if _, gone := m[x.r.ID.String()]; gone && x.r.Table == schema.TableActors {
			if x.file {
				mapped = append(mapped, store.Key{Table: x.r.Table, ID: x.r.ID})
			}
			continue
		}
		if p := mapActorFields(x.r.Fields, m); p != nil {
			x.r.Fields = maps.Clone(x.r.Fields)
			maps.Copy(x.r.Fields, p)
		}
		// the audit stamps too: the mapped actor has no row in the folder
		x.r.CreatedBy = mapActorID(x.r.CreatedBy, m)
		x.r.UpdatedBy = mapActorID(x.r.UpdatedBy, m)
		kept = append(kept, x)
	}
	return kept, mapped, nil
}

// mapActorID is id, or the actor m maps it onto.
func mapActorID(id ids.ID[ids.Actor], m map[string]ids.AnyID) ids.ID[ids.Actor] {
	if id.IsZero() {
		return id
	}
	to, ok := m[id.String()]
	if !ok {
		return id
	}
	if got, err := ids.As[ids.Actor](to); err == nil {
		return got
	}
	return id
}
