// stores_project.go — reading and writing rows of a project that does not
// live in this checkout: a store project (`--to <org>`, a client repo's
// linked project) or a personal folder (`--to me`).
//
// How it works, and why this shape: every existing command reads and
// writes rows through an ent client on a SQLite DB plus lsync's export to
// .lore/data. A store project gets the same machinery in a MIRROR folder
// outside every checkout (~/.lore/cache/stores/<host>/<owner>/<repo>/
// <branch>/<folder>): its .lore/data is refreshed from the bare clone's
// tip, its DB (named externalDBFile, so the normal per-checkout sync
// session never adopts it and never wires git hooks into whatever repo
// encloses ~/.lore) imports those files, the command writes into that DB,
// and flushExternalProjects exports the rows and commits exactly the
// changed files into the store (stores.Store.WriteRows) — at once, pushed
// (§3.2). Nothing of a store project ever touches the checkout's lore.db
// (privacy blocker 1, TEAM-019).
package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dbent"
	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/processlock"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/stores"
	"saas/pkg/aicoder/style"
)

// externalDBFile is the DB of a mirrored project. Not projresolve.ModeAFile
// on purpose: see the file comment.
const externalDBFile = "external.db"

// Mirror folder and file modes: internal content, readable by the user only.
const (
	externalDirMode  = 0o700
	externalFileMode = 0o600
)

// externalProject is one non-checkout project opened by this command.
type externalProject struct {
	// Label is how rows of this project are shown: [northwind/acme], [me].
	Label string
	// Root holds .lore/data and .lore/external.db.
	Root string
	// DBPath is the project's DB.
	DBPath string
	// base is the content of every row file at open time (path relative
	// to .lore/data); the flush commits only what differs from it.
	base map[string][]byte
	// commit stores the changed row files where the project really lives;
	// nil for a project whose mirror IS its home (a local personal store).
	commit func(ctx context.Context, rc stores.RowChanges) (string, error)
	// live is the store branch's live session (refs/lore/live/<branch> in
	// the bare clone); nil for a local personal store (no git) or when live
	// rows are disabled. Rows the router places live go there, the rest to
	// the store branch.
	live *liveSession
	// liveFolder is this project's folder inside the store live tree.
	liveFolder string
	// unlock releases the mirror's lock (held from open to flush, so two
	// lore processes never build or write one mirror at once, side effect
	// 32); nil when not held.
	unlock func()
}

// mirrorLockFile serialises lore processes on one mirror.
const mirrorLockFile = "mirror.lock"

// lockMirror takes the mirror's lock, waiting while another process holds it.
func lockMirror(root string) (func(), error) {
	path := filepath.Join(root, projresolve.MarkerDir, projresolve.StateDir, mirrorLockFile)
	deadline := time.Now().Add(stores.DefaultLockWait)
	for {
		l, err := processlock.Acquire(path)
		switch {
		case err == nil:
			return func() { _ = l.Release() }, nil // a release error is not actionable; the OS drops the flock at exit
		case errors.Is(err, processlock.ErrReadOnly):
			return func() {}, nil
		case !errors.Is(err, processlock.ErrLockHeld):
			return nil, errcodes.New(errcodes.Internal, "lock "+path).WithCause(err)
		}
		if time.Now().After(deadline) {
			return nil, errcodes.New(errcodes.DBLocked, "another lore process has held "+path+" for over "+stores.DefaultLockWait.String())
		}
		time.Sleep(mirrorLockPoll)
	}
}

// mirrorLockPoll is how often a waiting process retries the mirror lock.
const mirrorLockPoll = 25 * time.Millisecond

// openedExternal lists the projects this command opened, flushed by main
// after the command (like syncSessions; the CLI handles one command per
// process).
var openedExternal []*externalProject

// storeProjectRef names a store project to open.
type storeProjectRef struct {
	Org    *lorecfg.Org
	Store  lorecfg.Store
	Folder string
}

// findStoreFolder is F13's lookup: the store of org holding folder, or —
// for a folder that exists nowhere yet — the store it would be created in
// (the org_folder store for ".", else the first store).
func findStoreFolder(ctx context.Context, cfg *lorecfg.Config, org *lorecfg.Org, folder string) (storeProjectRef, error) {
	return findStoreFolderOpt(ctx, cfg, org, folder, false)
}

// findStoreFolderOpt is findStoreFolder with the coordinator's typo guard
// (Q-stores-2): a folder that exists in no listed store is refused unless
// create is set (an explicit --create) or it is a configured org_folder.
func findStoreFolderOpt(ctx context.Context, cfg *lorecfg.Config, org *lorecfg.Org, folder string, create bool) (storeProjectRef, error) {
	var existing []string
	if len(org.Stores) == 0 {
		return storeProjectRef{}, errcodes.New(errcodes.InvalidInput, "org "+org.Name+" lists no stores")
	}
	if folder == stores.RootFolder {
		for _, sc := range org.Stores {
			if sc.OrgFolder == stores.RootFolder {
				return storeProjectRef{Org: org, Store: sc, Folder: folder}, nil
			}
		}
	}
	m, err := newStoreManager(cfg, org)
	if err != nil {
		return storeProjectRef{}, err
	}
	for _, sc := range org.Stores {
		st, err := m.Open(ctx, sc.URL)
		if err != nil {
			return storeProjectRef{}, storeCLIError(err, "open the store")
		}
		if _, err := st.Fetch(ctx, false); err != nil {
			return storeProjectRef{}, storeCLIError(err, "fetch the store")
		}
		ok, err := st.FolderExists(ctx, sc.EffectiveBranch(), folder)
		if err != nil {
			return storeProjectRef{}, storeCLIError(err, "read the store")
		}
		if tip, found, terr := st.Tip(ctx, sc.EffectiveBranch()); terr == nil && found {
			if paths, perr := st.Paths(ctx, tip); perr == nil {
				existing = append(existing, stores.FoldersOf(paths)...)
			}
		}
		if ok {
			return storeProjectRef{Org: org, Store: sc, Folder: folder}, nil
		}
	}
	for _, sc := range org.Stores {
		if sc.OrgFolder == folder {
			// The configured org folder may be created on first use: it is
			// named in config, not typed on a command line.
			return storeProjectRef{Org: org, Store: sc, Folder: folder}, nil
		}
	}
	if create {
		return storeProjectRef{Org: org, Store: org.Stores[0], Folder: folder}, nil
	}
	return storeProjectRef{}, errcodes.New(errcodes.InvalidInput, fmt.Sprintf("%s/ is not a project in any of %s's stores (existing: %s); nothing written", folder, org.Name, strings.Join(existing, ", "))).
		WithHint("check the spelling; create a new store project on purpose with --create, or `lore org link " + org.Name + " --project " + folder + "`")
}

// openStoreProject mirrors a store project and registers it for the flush.
func openStoreProject(ctx context.Context, cfg *lorecfg.Config, ref storeProjectRef) (*externalProject, error) {
	m, err := newStoreManager(cfg, ref.Org)
	if err != nil {
		return nil, err
	}
	st, err := m.Open(ctx, ref.Store.URL)
	if err != nil {
		return nil, storeCLIError(err, "open the store")
	}
	branch := ref.Store.EffectiveBranch()
	if _, err := st.Fetch(ctx, false); err != nil {
		return nil, storeCLIError(err, "fetch the store")
	}
	if _, ok, err := st.Tip(ctx, branch); err != nil {
		return nil, storeCLIError(err, "read the store")
	} else if !ok {
		return nil, storeCLIError(fmt.Errorf("%w: %s has no branch %s; create it with `lore store init %s --branch %s`", stores.ErrNoBranch, st.Norm, branch, ref.Store.URL, branch), "open")
	}
	if err := st.Check(ctx, branch); err != nil {
		return nil, storeCLIError(err, "check the store")
	}
	files, _, err := st.ReadProject(ctx, branch, ref.Folder)
	if err != nil {
		return nil, storeCLIError(err, "read the store project")
	}
	stored := make(map[string][]byte, len(files))
	for k, v := range files {
		stored[k] = v
	}
	if err := ensureProjectRow(files, ref.Folder); err != nil {
		return nil, err
	}
	root, err := m.Layout.MirrorDir(ref.Store.URL, branch, ref.Folder)
	if err != nil {
		return nil, storeCLIError(err, "mirror path")
	}
	if p := openedMirror(root); p != nil {
		return p, nil // already open in this command (one lock, one flush)
	}
	sig := storeSignature(ref.Org)
	push := storeAutopush(cfg, ref.Org)
	p := &externalProject{
		Label: "[" + ref.Org.Name + "/" + ref.Folder + "]",
		Root:  root,
		commit: func(ctx context.Context, rc stores.RowChanges) (string, error) {
			res, err := st.WriteRows(ctx, branch, ref.Folder, rc, sig, !push)
			if err != nil {
				return "", storeCLIError(err, "write to the store")
			}
			switch {
			case res.Pushed:
				return "pushed", nil
			case res.Offline:
				return "offline: committed in the local clone, pushed by the next command", nil
			default:
				return "committed in the local clone (autopush off; `lore sync` pushes it)", nil
			}
		},
	}
	if !liveDisabled() {
		p.live = storeLiveSessionFor(ctx, st, branch, sig)
		p.liveFolder = storeLiveFolder(ref.Folder)
	}
	if err := p.materialize(ctx, files, stored); err != nil {
		return nil, err
	}
	openedExternal = append(openedExternal, p)
	return p, nil
}

// newStoreProjectFilesNamed is the template of a fresh store project
// folder: _meta.json pinning a new project id plus that project's row, so
// the engine materialises a usable project exactly as for a fresh clone.
func newStoreProjectFilesNamed(folder string) (map[string][]byte, error) {
	id, err := ids.New(ids.PrefixProject)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "mint project id").WithCause(err)
	}
	meta, err := (stores.StoreMeta{ProjectID: id}).Encode()
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "encode _meta.json").WithCause(err)
	}
	row, err := projectRowFile(id, folder)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"_meta.json": meta, "projects/" + id + ".json": row}, nil
}

// rootProjectName names the project of a store's root folder (".").
const rootProjectName = "org"

// projectRowFile is the canonical projects/<id>.json of a store project.
func projectRowFile(id, folder string) ([]byte, error) {
	now := cliClock().Now().UTC().Format(time.RFC3339Nano)
	name := folder
	if folder == stores.RootFolder {
		name = rootProjectName
	}
	row, err := canonjson.Encode(map[string]any{
		"_table": "projects", "_v": int64(lsync.FormatVersion), "archived_at": nil, "created_at": now,
		"id": id, "name": name, "origin_url": nil, "updated_at": now,
	})
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "encode project row").WithCause(err)
	}
	return row, nil
}

// ensureProjectRow completes a store project's files so the engine sees a
// usable project: a missing _meta.json gets a new project (a folder that
// does not exist yet), and a _meta.json whose project has no projects/
// row gets one (folders made by other tools). The additions are committed
// with the command's rows.
func ensureProjectRow(files map[string][]byte, folder string) error {
	metaB, ok := files["_meta.json"]
	if !ok {
		tmpl, err := newStoreProjectFilesNamed(folder)
		if err != nil {
			return err
		}
		for k, v := range tmpl {
			files[k] = v
		}
		return nil
	}
	meta, err := stores.ParseStoreMeta(metaB)
	if err != nil {
		return errcodes.New(errcodes.Internal, "read the project's _meta.json").WithCause(err)
	}
	if meta.ProjectID == "" {
		return errcodes.New(errcodes.InvalidInput, "store folder "+folder+" has a _meta.json without project_id")
	}
	if _, ok := files["projects/"+meta.ProjectID+".json"]; ok {
		return nil
	}
	row, err := projectRowFile(meta.ProjectID, folder)
	if err != nil {
		return err
	}
	files["projects/"+meta.ProjectID+".json"] = row
	return nil
}

// materialize makes the mirror's .lore/data equal files and brings its DB
// up to date with them (a full sync pass; creates the DB the first time).
// base is what the project's home holds now: files missing from it (a new
// folder's _meta.json and project row) are committed by the flush.
func (p *externalProject) materialize(ctx context.Context, files, base map[string][]byte) error {
	if p.unlock == nil {
		unlock, err := lockMirror(p.Root)
		if err != nil {
			return err
		}
		p.unlock = unlock
	}
	data := filepath.Join(p.Root, projresolve.MarkerDir, lsync.DataDirName)
	if err := os.MkdirAll(data, externalDirMode); err != nil {
		return errcodes.New(errcodes.Internal, "create mirror").WithCause(err)
	}
	existing := map[string]bool{}
	err := filepath.WalkDir(data, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(data, path)
		existing[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		return errcodes.New(errcodes.Internal, "scan mirror").WithCause(err)
	}
	for rel := range existing {
		if _, keep := files[rel]; !keep {
			if err := os.Remove(filepath.Join(data, filepath.FromSlash(rel))); err != nil {
				return errcodes.New(errcodes.Internal, "refresh mirror").WithCause(err)
			}
		}
	}
	for rel, b := range files {
		full := filepath.Join(data, filepath.FromSlash(rel))
		if cur, err := os.ReadFile(full); err == nil && bytes.Equal(cur, b) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), externalDirMode); err != nil {
			return errcodes.New(errcodes.Internal, "refresh mirror").WithCause(err)
		}
		if err := os.WriteFile(full, b, externalFileMode); err != nil {
			return errcodes.New(errcodes.Internal, "refresh mirror").WithCause(err)
		}
	}
	p.base = base
	p.DBPath = filepath.Join(p.Root, projresolve.MarkerDir, externalDBFile)
	if _, err := os.Stat(p.DBPath); err != nil {
		if err := createProjectDB(ctx, p.DBPath); err != nil {
			return err
		}
	}
	_, err = p.pass(ctx, false)
	return err
}

// pass runs one lsync pass on the mirror: a full pass imports file
// changes; dirtyOnly exports what the command wrote.
func (p *externalProject) pass(ctx context.Context, dirtyOnly bool) (lsync.Report, error) {
	db := dbent.InitDB(p.DBPath)
	defer func() { _ = db.Close() }() // read-mostly handle; a close error is not actionable here
	if err := dbent.ApplyPragmas(db); err != nil {
		return lsync.Report{}, errcodes.New(errcodes.Internal, "open mirror DB").WithCause(err)
	}
	o := p.syncOptions(db)
	o.DirtyOnly = dirtyOnly
	if p.live != nil && dirtyOnly {
		states, err := lsync.LiveStates(ctx, db)
		if err != nil {
			return lsync.Report{}, errcodes.New(errcodes.Internal, "read live state of "+p.Label).WithCause(err)
		}
		dirty, err := lsync.DirtyRefs(ctx, db)
		if err != nil {
			return lsync.Report{}, errcodes.New(errcodes.Internal, "read pending rows of "+p.Label).WithCause(err)
		}
		o.Divert = liveDivert(states, dirty)
	}
	rep, err := lsync.Reconcile(ctx, o)
	if err != nil {
		return rep, errcodes.New(errcodes.Internal, "sync "+p.Label).WithCause(err)
	}
	if p.live != nil {
		if len(rep.Diverted) > 0 {
			if err := p.live.store(withStoreWrite(ctx), db, p.liveFolder, rep.Diverted); err != nil {
				return rep, errcodes.New(errcodes.Internal, "write the store live ref").WithCause(err)
			}
		}
		// Live wins: the store live folder is applied over the branch data.
		p.live.apply(ctx, db, p.liveFolder)
	}
	return rep, nil
}

// storeLiveFolder is a store project's folder in the store live tree
// (§2: the store root's project is _root/).
func storeLiveFolder(folder string) string {
	if folder == stores.RootFolder {
		return rootLiveFolder
	}
	return folder
}

func (p *externalProject) syncOptions(db *sql.DB) lsync.Options {
	return lsync.Options{
		DB:           db,
		DataDir:      filepath.Join(p.Root, projresolve.MarkerDir, lsync.DataDirName),
		LockPath:     filepath.Join(p.Root, projresolve.MarkerDir, projresolve.StateDir, syncLockFile),
		DBChangeKind: lsync.ChangeLocal,
		OnChange:     auditChange,
		CheckSecrets: secretCheck(),
		Now:          func() time.Time { return cliClock().Now() },
	}
}

// flush exports the command's writes, commits the changed branch files,
// then pushes the store live ref and lands what is due on the store branch.
func (p *externalProject) flush(ctx context.Context) error {
	err := p.flushBranch(ctx)
	if p.live != nil {
		if !p.live.finished {
			p.live.finishStore(withStoreWrite(ctx), p.liveFolder)
		} else {
			// The session was finished by another folder of the same store
			// branch this command: land this folder too, then push.
			p.live.autoLand(withStoreWrite(ctx), p.liveFolder)
			p.live.push(withStoreWrite(ctx), false)
		}
	}
	return err
}

// flushBranch is the branch-data half of flush.
func (p *externalProject) flushBranch(ctx context.Context) error {
	rep, err := p.pass(ctx, true)
	if err != nil {
		return err
	}
	for _, fe := range rep.Errors {
		fmt.Fprintln(os.Stderr, style.Warn("lore: "+p.Label+" "+fe.Path+": "+fe.Detail))
	}
	rc, err := p.diff()
	if err != nil {
		return err
	}
	if len(rc.Put)+len(rc.Delete) == 0 || p.commit == nil {
		return nil
	}
	state, err := p.commit(ctx, rc)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "lore: %s %d file(s) %s\n", p.Label, len(rc.Put)+len(rc.Delete), state)
	return nil
}

// diff compares the mirror's .lore/data with the content at open time.
func (p *externalProject) diff() (stores.RowChanges, error) {
	data := filepath.Join(p.Root, projresolve.MarkerDir, lsync.DataDirName)
	rc := stores.RowChanges{Put: map[string][]byte{}}
	seen := map[string]bool{}
	err := filepath.WalkDir(data, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(data, path)
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(rel, ".tmp") {
			return nil
		}
		seen[rel] = true
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if old, ok := p.base[rel]; !ok || !bytes.Equal(old, b) {
			rc.Put[rel] = b
		}
		return nil
	})
	if err != nil {
		return rc, errcodes.New(errcodes.Internal, "scan mirror").WithCause(err)
	}
	for rel := range p.base {
		if !seen[rel] {
			rc.Delete = append(rc.Delete, rel)
		}
	}
	sort.Strings(rc.Delete)
	return rc, nil
}

// storeLiveSessions holds ONE live session per (bare clone, store branch)
// for this command: two mirrors of one store branch (the linked project and
// the org folder's roster) share it, because a second session would wait
// out the first one's live lock in the same process (~30 s).
var storeLiveSessions = map[string]*liveSession{}

// storeLiveSessionFor returns the command's live session for a store
// branch, preparing it (fetch + integrate) once.
func storeLiveSessionFor(ctx context.Context, st *stores.Store, branch string, sig gitref.Signature) *liveSession {
	key := st.Dir + "\x00" + branch
	if s := storeLiveSessions[key]; s != nil {
		return s
	}
	s := newStoreLiveSession(ctx, st.Repo, st.Remote, stores.RemoteName, filepath.Join(st.Dir, storeStateDir), branch, sig)
	s.prepare(ctx, liveCmdState.fetchKind())
	storeLiveSessions[key] = s
	return s
}

// releaseStoreLiveSessions drops every store live lock at the end of the
// command.
func releaseStoreLiveSessions() {
	for k, s := range storeLiveSessions {
		s.release()
		delete(storeLiveSessions, k)
	}
}

// flushExternalProjects runs after every command (main): each opened
// external project's writes reach their home. Errors are printed; the
// command's own exit status is decided by the command.
func flushExternalProjects(ctx context.Context) error {
	var first error
	for _, p := range openedExternal {
		err := p.flush(ctx)
		if p.unlock != nil {
			p.unlock()
			p.unlock = nil
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, style.Error("ERROR: ")+redactIfQuiet(err.Error()))
			if first == nil {
				first = err
			}
		}
	}
	openedExternal = nil
	releaseStoreLiveSessions()
	return first
}

// openedMirror returns the project already opened at root by this command.
func openedMirror(root string) *externalProject {
	for _, p := range openedExternal {
		if p.Root == root {
			return p
		}
	}
	return nil
}

// useExternalProject points a command's scope flags at an external
// project's DB, so the command's normal code writes there and nowhere else.
func useExternalProject(c *commonFlags, p *externalProject) {
	c.flagDB = p.DBPath
	c.flagProject = ""
	c.flagRepo = ""
}
