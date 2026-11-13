package wire

// storefolders.go — the store commands that need only the setup, outside
// any repo or in a repo they do not change (owner report 2026-11-11: an
// admin keeps the client folders in a plain parent directory, not a git
// repo, and makes the team store's folders from there).
//
//	App.Run ─▶ opsstores.RunsWithoutRepo(op) and no lore repo here
//	          ─▶ runSetupOnly: the machine-only engine (policies of this
//	             machine, confirm), the op's Manager is this App, whose
//	             store lookups read the setup alone (storeRepoTop = "")
//
//	lookupStore(name) ─▶ team     the active setup's org store (orgAliases)
//	                  ├▶ <url>    the configured org store at that address
//	                  ├▶ me       personal.store
//	                  ├▶ <org>    a store the setup's org brings
//	                  └▶ <name>   .lore/stores.yaml: needs a repo; outside
//	                              one the error lists the names that work
//
// ListFolders (store folder list) reads each store's folders from its
// local copy (a folder store's path, the lore-owned clone of a git store).

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

var _ opsstores.FolderLister = (*App)(nil)

// gitDirName is the folder git keeps a clone's history in; a store walk
// never descends into it.
const gitDirName = ".git"

// createdFormat is the git log format of a folder's first commit: author
// name, then the author date (strict ISO 8601), tab-separated.
const createdFormat = "%an%x09%aI"

// setupOnlyHere reports whether the store operation name runs on the
// setup alone here: it needs no repo (opsstores.RunsWithoutRepo) and the
// command does not run inside a lore repo. In a lore repo it keeps its
// repo-only engine (the repo's own policies); in a plain folder, or a git
// repo that is not a lore project, nothing of that folder is opened, so
// nothing is created or wired there.
func (a *App) setupOnlyHere(ctx context.Context, name string) bool {
	if !opsstores.RunsWithoutRepo(name) {
		return false
	}
	return !insideWorkTree(ctx, a.opts.RepoDir) || inStore(a.opts.RepoDir) || !opssetup.LoreRepoAt(a.opts.RepoDir)
}

// refuseStoresFileOutsideRepo answers a store command that edits a repo's
// .lore/stores.yaml (init of a store other than me, add, remove, set-url)
// run outside any git work tree with what it needs, before anything else
// asks: it used to be the CFG7 question "this is a client repo" (no repo
// counts as a client, fail closed), which named a repo that is not there.
func (a *App) refuseStoresFileOutsideRepo(ctx context.Context, name string) error {
	if !opsstores.WritesStoresFile(name) && name != opsstores.OpRemove {
		return nil
	}
	if sc, in := storeHere(a.opts.RepoDir); in {
		verb := "store " + strings.ReplaceAll(strings.TrimPrefix(name, storeOpPrefix), "_", "-")
		return errs.New(errs.NotAStore, verb+": it edits a repo's .lore/stores.yaml, and "+sc.top+" is a lore store, not a code repo").
			WithHint("cd into the code repo that should attach the store")
	}
	if insideWorkTree(ctx, a.opts.RepoDir) {
		return nil
	}
	verb := "store " + strings.ReplaceAll(strings.TrimPrefix(name, storeOpPrefix), "_", "-")
	return errs.New(errs.NotAStore, verb+": it edits a repo's .lore/stores.yaml, and "+a.opts.RepoDir+" is not inside a git repo").
		WithHint("cd into the repo that should attach the store; outside a repo, `lore store folder add --store team --folder <folder>` and `lore store list` work from the setup alone (`lore store init --name me` makes the personal store)")
}

// storeOpPrefix starts every store operation name.
const storeOpPrefix = "store."

// runSetupOnly runs a store operation on the machine-only engine (this
// machine's policies, the caller's confirm), with this App as its Manager.
func (a *App) runSetupOnly(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	a.mu.Lock()
	if !a.machOpened {
		a.machOpened = true
		a.mach, a.machErr = openMachineOnly(ctx, a.opts)
	}
	mach, err := a.mach, a.machErr
	a.mu.Unlock()
	if err != nil {
		return op.Result{}, err
	}
	eng, err := mach.Engine(ctx)
	if err != nil {
		return op.Result{}, err
	}
	// AGENT_IDENTITY 8.2: the detected agent, from the machine-only open's
	// identity (App.Run's own detection would open the repo), so
	// agents.person_only fires here too
	if !c.AgentFixed && c.Agent == "" && c.Actor.IsZero() {
		c.Agent = mach.CallerAgent(ctx)
	}
	return eng.Run(ctx, e, d, c)
}

// lookupStore is the store a store command names (see the file comment),
// read-only attachments included. Unknown is E_NOT_FOUND; outside a repo
// the error says that .lore/stores.yaml names need one and lists the
// names that work here.
func (a *App) lookupStore(ctx context.Context, verb, name string) (attach.Store, error) {
	if opsstores.IsStoreURL(name) {
		return a.orgStoreAt(ctx, verb, name)
	}
	top, err := a.storeRepoTop(ctx)
	if err != nil {
		return attach.Store{}, err
	}
	look := name
	if name == attach.ReservedTeam {
		alias, ok := orgAliases(ctx, a.opts, top)[name]
		if !ok {
			if top == "" {
				return attach.Store{}, errs.New(errs.NotFound, verb+": this folder is in no setup with a team store").
					WithHint("run it in a folder of an admin or member setup (`lore setup list` shows each setup's folders), or pass --setup <name>")
			}
			return attach.Store{}, errs.New(errs.NotFound, verb+": this repository has no team store").
				WithHint("link the repo to an org (`lore org link --org <org> --project <folder>`), or name the store")
		}
		look = string(alias)
	}
	src, found, _, err := a.baseStore(ctx, look)
	if err != nil {
		return attach.Store{}, err
	}
	if !found {
		if top == "" && name != attach.ReservedMe {
			return attach.Store{}, a.needsRepoErr(ctx, verb, name)
		}
		return attach.Store{}, errs.New(errs.NotFound, verb+": no store named "+name+" is attached").
			WithHint("run `lore store list`")
	}
	return src, nil
}

// needsRepoErr is the refusal of a store name outside any repo: only a
// repo's .lore/stores.yaml gives such a name, so it says so, and names
// what does work in this folder (exact errors: the cause and the next
// step).
func (a *App) needsRepoErr(ctx context.Context, verb, name string) error {
	work := a.namesHere(ctx)
	hint := "cd into the repo whose .lore/stores.yaml attaches " + name
	if len(work) > 0 {
		hint = "here name " + strings.Join(work, ", ") + "; or " + hint
	}
	return errs.New(errs.NotFound, verb+": no store named "+name+" here: "+name+" would come from a repo's .lore/stores.yaml, and "+a.opts.RepoDir+" is not inside a git repo").
		WithHint(hint)
}

// namesHere are the store names a store command takes with no repo: team
// (when the setup has an org store), every org store name the setup
// brings, me (when a personal store is set), and the org stores' urls.
func (a *App) namesHere(ctx context.Context) []string {
	var out []string
	add := func(n string) {
		if n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	if _, ok := orgAliases(ctx, a.opts, "")[attach.ReservedTeam]; ok {
		add(attach.ReservedTeam)
	}
	org := rawOrgStores(ctx, a.opts)
	for _, s := range org {
		if !strings.Contains(string(s.Name), targetSep) { // <org>/<folder> names a folder, not a store
			add(string(s.Name))
		}
	}
	if mc, err := readMachineConfig(a.opts.Home); err == nil && effectivePersonalURL(ctx, a.opts.Home, a.projectDir(), mc) != "" {
		add(attach.ReservedMe)
	}
	for _, s := range org {
		add(s.URL)
	}
	return out
}

// orgStoreAt is the org store a url names: one the active setup's org
// brings (its configured url and branch), else any configured org's store
// at that address. The url must be an org store's: an arbitrary url is
// never written to (`lore store add` attaches one to a repo first).
func (a *App) orgStoreAt(ctx context.Context, verb, raw string) (attach.Store, error) {
	want, ok := storeKey(raw)
	if !ok {
		return attach.Store{}, errs.New(errs.InvalidInput, verb+": "+raw+" is not a store url lore can read").
			WithHint("a git url (git@host:owner/repo, ssh://, https://) or file:///path")
	}
	same := func(u string) bool { k, kok := storeKey(u); return kok && k == want }
	for _, s := range rawOrgStores(ctx, a.opts) {
		if same(s.URL) {
			org, _, _ := strings.Cut(string(s.Name), targetSep)
			return attach.Store{Name: store.Source(org), URL: s.URL, Branch: s.Branch}, nil
		}
	}
	if a.opts.OrgStoreURLs != nil {
		if all, err := a.opts.OrgStoreURLs(ctx); err == nil {
			for _, s := range all {
				if same(s.URL) {
					return attach.Store{Name: store.Source(s.Org), URL: s.URL}, nil
				}
			}
		}
	}
	return attach.Store{}, errs.New(errs.NotFound, verb+": "+raw+" is not the store of an org in your setups").
		WithHint("name an org store's url as `lore store show` lists it, or attach the store to a repo with `lore store add --name <name> --url <url>`")
}

// storeLabel is the name a store command's output gives the store name
// resolved to: the org's name for team (the org's team store), an org
// store named by its url gets its short name (storeshortname.go), else
// name as given. An org store is never labelled by its org's name alone:
// an org may have several stores, and the output must name the one
// written to, not the org (owner report 2026-11-11: "created folder x in
// <org>" for the org's second store).
func (a *App) storeLabel(name string, src attach.Store) string {
	if name == attach.ReservedTeam {
		if org, _, _ := strings.Cut(string(src.Name), targetSep); org != "" {
			return org
		}
	}
	if opsstores.IsStoreURL(name) {
		return a.shortLabel(name)
	}
	return name
}

// ListFolders implements opsstores.FolderLister.
func (a *App) ListFolders(ctx context.Context, name string) (opsstores.FolderListOut, error) {
	const verb = "store folder list"
	type one struct {
		label string
		src   attach.Store
	}
	var stores []one
	if name != "" {
		src, err := a.lookupStore(ctx, verb, name)
		if err != nil {
			return opsstores.FolderListOut{}, err
		}
		stores = append(stores, one{a.storeLabel(name, src), src})
	} else {
		seen := map[string]bool{}
		used := map[string]bool{}
		orgOf, orgOfAny := a.orgFolderStores(), a.storeOrgs()
		for _, e := range a.storesHere(ctx) {
			k := placeKey(e.URL, e.Branch)
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			label := a.placeLabel(e.Name, e.URL, orgOf, orgOfAny)
			if used[label] {
				label = a.urlLabel(e.URL) // two places, one name: the url tells them apart
			}
			used[label] = true
			stores = append(stores, one{label, attach.Store{Name: store.Source(label), URL: e.URL, Branch: e.Branch}})
		}
	}
	attached := a.attachedFolders(ctx)
	var out opsstores.FolderListOut
	for _, s := range stores {
		out.Stores = append(out.Stores, s.label)
		dir, err := a.storeDir(ctx, s.src)
		if err != nil {
			return opsstores.FolderListOut{}, errs.Prefix(verb+": store "+s.label, err)
		}
		rows, err := storeFolders(ctx, dir)
		if err != nil {
			return opsstores.FolderListOut{}, errs.Prefix(verb+": store "+s.label, err)
		}
		k := placeKey(s.src.URL, s.src.Branch)
		for i := range rows {
			rows[i].Store = s.label
			rows[i].Attached = attached[k+"\x00"+rows[i].Folder]
		}
		out.Folders = append(out.Folders, rows...)
	}
	return out, nil
}

// placeLabel is the name store folder list heads a store's block with:
// the store that really holds the folders (owner report 2026-11-12: a
// repo linked to a folder of the org's second store printed "store <org>
// folders:" for that store, because its entry is named after the org, and
// so printed the org's block twice). The org's name for the store holding
// the org folder, the short name for the org's other stores (brief 3.6;
// the url when no configured org lists it), else the entry's
// own name (a stores.yaml store, me).
func (a *App) placeLabel(name, url string, orgOf, orgOfAny map[string]string) string {
	if sk, ok := storeKey(url); ok {
		if org := orgOf[sk]; org != "" {
			return org
		}
		if orgOfAny[sk] != "" {
			return a.shortLabel(url)
		}
	}
	if opsstores.IsStoreURL(name) {
		return a.shortLabel(name)
	}
	label, _, _ := strings.Cut(name, targetSep)
	return label
}

// urlLabel is how store folder list names an org store known only by its
// url: the url itself, in a pipe and in CI too (a store's address is no
// secret), with only a credential in it masked (repourl.Mask).
func (a *App) urlLabel(raw string) string { return repourl.Mask(raw) }

// storesHere are the stores this command sees: the repo's (stores.yaml
// and its org's, when there is a repo) and the setup's (its org's stores,
// me). Errors leave a source out; the listing names what it read.
func (a *App) storesHere(ctx context.Context) []opsstores.Entry {
	got, _ := a.ListStores(ctx)
	org, _ := a.OrgStoreEntries(ctx)
	var personal []opsstores.Entry
	var head []opsstores.Entry
	for _, e := range got {
		if e.Personal {
			personal = append(personal, e)
			continue
		}
		head = append(head, e)
	}
	return slices.Concat(org, head, personal)
}

// attachedFolders are the store folders the repo here attaches, keyed
// placeKey + NUL + folder; none outside a repo.
func (a *App) attachedFolders(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	if top, err := a.storeRepoTop(ctx); err != nil || top == "" {
		return out
	}
	got, _ := a.ListStores(ctx)
	org, _ := a.OrgStoreEntries(ctx)
	for _, e := range slices.Concat(got, org) {
		if f := rootFolder(e.Folder); f != "" && !e.Personal {
			out[placeKey(e.URL, e.Branch)+"\x00"+f] = true
		}
	}
	return out
}

// placeKey identifies a store place: its url however spelled (storeKey),
// and its branch. "" for a url lore cannot read.
func placeKey(raw, branch string) string {
	k, ok := storeKey(raw)
	if !ok {
		return ""
	}
	return k + "#" + branch
}

// storeFolders lists the folders of the store at dir (rowfs.StoreFolders:
// every folder holding a _meta.json, the root excepted), sorted by name.
func storeFolders(ctx context.Context, dir string) ([]opsstores.FolderRow, error) {
	isGit := false
	if fi, err := os.Stat(filepath.Join(dir, gitDirName)); err == nil {
		isGit = fi.IsDir() || fi.Mode().IsRegular() // a worktree's .git is a file
	}
	names, err := rowfs.StoreFolders(dir)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "read the store's folders", err)
	}
	out := make([]opsstores.FolderRow, 0, len(names))
	for _, name := range names {
		row, ok, rerr := folderRow(ctx, dir, name, isGit)
		if rerr != nil {
			return nil, errs.Wrap(errs.Internal, "read the store's folders", rerr)
		}
		if ok {
			out = append(out, row)
		}
	}
	return out, nil
}

// folderRow reads one folder rel of the store at dir; ok is false when it
// is no folder of the store (no _meta.json).
func folderRow(ctx context.Context, dir, rel string, isGit bool) (opsstores.FolderRow, bool, error) {
	clean, err := rowfs.CleanFolder(rel)
	if err != nil {
		return opsstores.FolderRow{}, false, nil // not a name lore makes: not listed
	}
	if clean == rowfs.FolderRoot || !rowfs.HasStoreFolder(dir, clean) {
		return opsstores.FolderRow{}, false, nil
	}
	r, err := rowfs.Open(dir)
	if err != nil {
		return opsstores.FolderRow{}, false, err
	}
	f, err := r.Folder(clean)
	if err != nil {
		return opsstores.FolderRow{}, false, err
	}
	id, err := f.ProjectID(ctx)
	if err != nil {
		return opsstores.FolderRow{}, false, err
	}
	n, _ := f.RowFileCount() // display only: an unreadable folder counts nothing
	row := opsstores.FolderRow{Folder: clean, ID: id.String(), Rows: n}
	if isGit {
		row.CreatedBy, row.CreatedAt = firstCommit(ctx, dir, f.Layout().StorePath(clean, rowfs.MetaFileName))
	}
	return row, true, nil
}

// firstCommit is the author and date of the commit that added rel in the
// clone at dir ("" both when git cannot tell: display only).
func firstCommit(ctx context.Context, dir, rel string) (by, at string) {
	out, err := gitRun{dir: dir, args: []string{"log", "--diff-filter=A", "--format=" + createdFormat, "--", rel}}.output(ctx)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	by, at, _ = strings.Cut(lines[len(lines)-1], "\t") // the oldest add
	if t, perr := time.Parse(time.RFC3339, at); perr == nil {
		at = t.UTC().Format(time.RFC3339)
	}
	return by, at
}
