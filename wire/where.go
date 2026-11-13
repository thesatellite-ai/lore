package wire

// where.go — `lore where` (ops/where; plain files sweep 3.2, 2026-11-12):
// every path lore owns on this machine and in the repo the command runs
// in, one entry each with what it is. It only reads (files and git
// config), opens no store, and names anything in the lore home that
// homeLayout does not know instead of hiding it.
//
//	home   ─▶ walk <home> against homeLayout (store clones and caches get
//	          their own description: the store's url, the store and repos
//	          a cache serves, from the cache use records)
//	stores ─▶ every clone (git.ScanClones, own clones included): the
//	          clone, its .git/lore bookkeeping, lore's lines in
//	          .git/info/attributes and .git/config; a folder store
//	repo   ─▶ .lore/ (rows at .lore/data, an old cache, lock, state,
//	          LORE.md, stores.yaml), lore's blocks in .gitignore,
//	          .gitattributes, the agents' pointer and private files and
//	          settings, .git/info/exclude, .git/config keys, hook blocks,
//	          .git/lore files and lore's refs
//	user   ─▶ the skill folders that exist, the lore binary

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opswhere "github.com/thesatellite-ai/lore/ops/where"
)

var _ opswhere.Env = (*App)(nil)

// Markers lore writes into files it shares with a person (each file's
// own writer defines it; the listing only looks for them).
const (
	whereGitBlock     = "# >>> lore-sync"
	whereIgnoreHeader = "# lore"
	wherePointerStart = toolprofile.PointerStartMarker
	whereContextStart = toolprofile.ContextStartMarker
	whereHookWord     = "lore "
	whereExcludeWord  = "# lore"
	whereRowAttribute = "-merge"
)

// whereRefPrefixes are the ref namespaces lore keeps in a repo or clone.
var whereRefPrefixes = []struct{ prefix, what string }{
	{"refs/lore/", "lore's live rows and claim signals (shared with teammates through the remote)"},
	{"refs/lore-remote/", "the remote's copy of lore's refs as last fetched"},
	{"refs/lore-backup/", "backups lore made before a migration, a live-ref compaction or a store compaction (local only)"},
}

// whereGitLore describes the files in a .git/lore folder by name.
var whereGitLore = []struct{ pattern, what string }{
	{"lock", "the store lock (holds no data)"},
	{"state", "the store lock's journal of audit records not yet acknowledged"},
	{"audit-pending.jsonl", "the journal of audit records not yet acknowledged (a folder store's)"},
	{"fetch-stamp", "when lore last fetched the remote"},
	{"remote-tip", "the store branch's tip on the remote when lore last saw it, so a store repo deleted and made again is noticed, never refilled"},
	{"remote-reset", "set when lore found the store's repo deleted and made again; nothing is pushed until lore store push --restore or lore store forget"},
	{gitbackend.IdentityRecordFile, "the user.email lore wrote into this clone's .git/config, so lore keeps only its own value current and never changes one you set"},
	{"push_stamp", "when lore last pushed"},
	{"push_fail_stamp", "when lore's last batch push failed"},
	{waitSaidFile, "how many unpushed commits lore last told you about, so a read says it once, not every time"},
	{"live-waiting-said", "the last \"not pushed yet\" line lore printed, so it is said once, not on every command"},
	{"live-remote-fault", "why the last live push could not reach the remote (no repository there, refused credentials); lore doctor warns with it"},
	{"compact_stamp", "when lore last compacted the store's history"},
	{"git-wiring*", "a fingerprint of lore's git wiring (merge driver, hooks), so it is checked only when something changed"},
	{"live.lock", "the lock of the live ref"},
	{"live.log", "the log of live ref operations"},
	{"refs-lore-live.*", "live writes not yet confirmed by the remote"},
	{"held.json", "the claims this machine holds"},
	{"signals-seen.json", "when this clone first saw each teammate's claim (a claim counts as expired only after it was watched)"},
	{"inbox-*.json", "the inbox entries you acknowledged"},
	{"_purged.json", "ids purged on purpose"},
	{"empty-hooks", "an empty hook folder lore's own commits run with (it validates before it commits)"},
}

// Where implements opswhere.Env.
func (a *App) Where(ctx context.Context, in opswhere.ShowIn) (opswhere.Out, error) {
	home := a.opts.Home
	out := opswhere.Out{Home: home, Entries: []opswhere.Entry{}}
	if readme := filepath.Join(home, HomeReadmeFile); fileExists(readme) {
		out.Readme = readme
	}
	uses := whereUses(home)
	out.Entries, out.Unknown = whereHome(home, uses, in.All)
	out.Entries = append(out.Entries, a.whereStores(ctx, home)...)
	if top := whereRepoTop(ctx, a.opts.RepoDir); top != "" {
		if storeTop, _, isStore := rowfs.StoreTop(top); !isStore || storeTop != top {
			out.Repo = top
			out.Entries = append(out.Entries, whereRepo(ctx, top)...)
			// the repo's cache sits under the lore home, never in the repo
			// (owner item 16); named here so the repo's list says where
			if c := RepoCachePath(home, top); fileExists(c) {
				out.Entries = append(out.Entries, opswhere.Entry{Area: opswhere.AreaRepo, Path: c, Kind: opswhere.KindFile, Exists: true,
					What: "this repo's search cache, kept under the lore home so no command creates a file in the repo; rebuilt from the rows", ByHand: byHandRebuilt})
			}
		}
	}
	out.Entries = append(out.Entries, whereUser()...)
	return out, nil
}

// whereRepoTop is the git top of dir ("" outside a work tree).
func whereRepoTop(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	b, err := gitRun{dir: dir, args: []string{"rev-parse", "--show-toplevel"}}.output(ctx)
	if err != nil {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(b))))
}

// cacheUse is what the use records say about one cache file.
type cacheUse struct {
	stores []string
	repos  []string
}

// whereUses maps each cache file (slash path relative to <home>/cache)
// to the stores and repos that use it, from the cache use records.
func whereUses(home string) map[string]*cacheUse {
	m := map[string]*cacheUse{}
	recs, err := sqlite.ReadUses(filepath.Join(home, cacheDir))
	if err != nil {
		return m
	}
	at := func(path string) *cacheUse {
		c := m[path]
		if c == nil {
			c = &cacheUse{}
			m[path] = c
		}
		return c
	}
	for _, r := range recs {
		if r.Record.Repo == "" {
			continue // a damaged record names nothing
		}
		repo := r.Record.Repo
		if !loreFolderAt(repo) {
			// The checkout moved or was deleted: the record is stale until
			// `lore cache prune` drops it, so say so instead of listing a
			// path that is not there.
			repo += whereGoneRepo
		} else {
			// The repo store's own cache is named from the checkout's path
			// (not in the record): show who uses it too.
			own := at(sqlite.RepoCacheRel(r.Record.Repo))
			own.repos = appendNew(own.repos, repo)
		}
		for _, u := range r.Record.Caches {
			c := at(u.Path)
			c.stores = appendNew(c.stores, u.Store)
			c.repos = appendNew(c.repos, repo)
		}
	}
	return m
}

// whereGoneRepo marks a recorded repo path that no longer holds a lore
// repo (B3-9: a moved checkout was still listed as a user of its caches).
const whereGoneRepo = " (gone: moved or deleted; `lore cache prune` drops it)"

// appendNew appends s to list unless it is empty or already there.
func appendNew(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	if s == "" {
		return list
	}
	return append(list, s)
}

// whereHome walks home against homeLayout: the entries found, and the
// paths no entry matches (with all, every Readme entry that is not there
// yet is listed too).
func whereHome(home string, uses map[string]*cacheUse, all bool) ([]opswhere.Entry, []string) {
	var entries []opswhere.Entry
	var unknown []string
	seen := map[string]bool{}
	_ = filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == home {
			return nil // an unreadable folder is skipped, never an error of the listing
		}
		rel, rerr := filepath.Rel(home, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		e, ok := matchHome(rel, d.IsDir())
		switch {
		case ok:
			seen[e.Pattern] = true
			entries = append(entries, opswhere.Entry{Area: opswhere.AreaHome, Path: p, Kind: whereKind(d.IsDir()), What: whereHomeWhat(e, rel, uses), ByHand: e.ByHand, Exists: true, Collapsed: d.IsDir() && e.Collapse})
			if d.IsDir() && e.Collapse {
				return filepath.SkipDir
			}
		case d.IsDir():
			// a folder with no entry of its own (a store id under
			// cache/stores): its files are matched one by one
		default:
			unknown = append(unknown, p)
		}
		return nil
	})
	if all {
		for _, e := range homeLayout {
			if e.Readme && !seen[e.Pattern] && !strings.ContainsAny(e.Pattern, "*?[") {
				entries = append(entries, opswhere.Entry{Area: opswhere.AreaHome, Path: filepath.Join(home, filepath.FromSlash(e.Pattern)), Kind: whereKind(e.Dir), What: e.What, ByHand: e.ByHand})
			}
		}
	}
	return entries, unknown
}

// whereKind is the entry kind of a folder or a file.
func whereKind(dir bool) string {
	if dir {
		return opswhere.KindFolder
	}
	return opswhere.KindFile
}

// whereHomeWhat is e's description of rel, with the stores and repos a
// cache file serves.
func whereHomeWhat(e homeEntry, rel string, uses map[string]*cacheUse) string {
	cacheRel, isCache := strings.CutPrefix(rel, cacheDir+"/")
	if !isCache {
		return e.What
	}
	for _, suf := range []string{"-wal", "-shm"} {
		cacheRel = strings.TrimSuffix(cacheRel, suf)
	}
	c := uses[cacheRel]
	if c == nil {
		return e.What
	}
	what := e.What
	if len(c.stores) > 0 {
		what += "; store " + strings.Join(c.stores, ", ")
	}
	if len(c.repos) > 0 {
		what += "; used by " + strings.Join(c.repos, ", ")
	}
	return what
}

// whereStores lists every store clone and folder store on this machine,
// each clone's entries tagged with the store's short name.
func (a *App) whereStores(ctx context.Context, home string) []opswhere.Entry {
	var out []opswhere.Entry
	// the personal store's clone is named me, as store list names it
	personal := ""
	if mc, merr := readMachineConfig(a.opts.Home); merr == nil {
		if p := effectivePersonalURL(ctx, a.opts.Home, a.projectDir(), mc); p != "" {
			if k, ok := storeKey(p); ok {
				personal = k
			}
		}
	}
	clones, err := gitbackend.ScanClones(home)
	if err == nil {
		for _, c := range clones {
			label := ""
			if c.Origin != "" {
				label = a.shortLabel(c.Origin)
				if k, ok := storeKey(c.Origin); ok && personal != "" && k == personal {
					label = opsstores.PersonalName
				}
			}
			for _, e := range whereClone(ctx, c, label) {
				e.Store = label
				out = append(out, e)
			}
		}
	}
	root := filepath.Join(home, gitbackend.StoresDir)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == root {
			return nil
		}
		if _, gerr := os.Lstat(filepath.Join(p, ".git")); gerr == nil {
			return filepath.SkipDir // a clone, listed above
		}
		if _, merr := os.Stat(filepath.Join(p, rowfs.MarkerFile)); merr == nil {
			e := opswhere.Entry{Area: opswhere.AreaStore, Path: p, Kind: opswhere.KindFolder, Exists: true, Collapsed: true,
				What:   "a store that is a plain folder (no git): rows are JSON files at " + whereRowPattern(p),
				ByHand: "your data: rows may be opened and edited; deleting the folder deletes the store"}
			if p == personalFolderDir(home) {
				e.Store = opsstores.PersonalName
				e.What = "your personal store " + opsstores.PersonalName + ", a plain folder (no git): rows are JSON files at " + whereRowPattern(p)
			}
			out = append(out, e)
			out = append(out, whereStateDir(filepath.Join(p, rowfs.StoreStateDir), opswhere.AreaStore,
				"lore's own files for this folder store (the store lock, its journal): not rows, never read as data")...)
			return filepath.SkipDir
		}
		return nil
	})
	return out
}

// whereRowPattern is where one row of a store folder lives, under top,
// from the row layout itself (rowfs), so a layout change shows here.
func whereRowPattern(top string) string {
	return filepath.Join(top, filepath.FromSlash(rowfs.LayoutStore.DataPath("<folder>")), "<table>", "<id>"+rowfs.RowFileExt) +
		" (the store's own rows: " + filepath.Join(top, filepath.FromSlash(rowfs.LayoutStore.DataPath(rowfs.FolderRoot)), "<table>", "<id>"+rowfs.RowFileExt) + ")"
}

// whereClone is one clone's entries.
func whereClone(ctx context.Context, c gitbackend.CloneFound, label string) []opswhere.Entry {
	name := c.Origin
	if label != "" && label != repourl.Mask(c.Origin) {
		name = label + " (" + repourl.Mask(c.Origin) + ")"
	} else if c.Origin != "" {
		name = repourl.Mask(c.Origin)
	}
	what := "a git clone of store " + name
	switch {
	case c.Own:
		what = "your own git clone of store " + name + " (lore store use; recorded in state/" + gitbackend.CloneRecordFile + ")"
	case c.Origin == "":
		what = "a store clone with no origin"
	}
	what += "; rows are JSON files at " + whereRowPattern(c.Paths.Dir) + "; " + rowfs.ReadmeFile + " at its top says how to browse and edit it; " + rowfs.SignpostFile + " there is for lore 0.1.17, which names it when it refuses the store (it says to upgrade)"
	if c.State != gitbackend.CloneWhole {
		what += "; state: " + string(c.State) + " (`lore doctor` says what to do)"
	}
	byHand := "rows may be opened and edited with any editor; a clone with no unpushed work is safe to delete (lore clones it again), `lore store forget` removes it cleanly"
	if c.Own {
		byHand = "your clone: lore never deletes it; `lore store use --default <store>` stops using it"
	}
	out := []opswhere.Entry{{Area: opswhere.AreaStore, Path: c.Paths.Dir, Kind: opswhere.KindFolder, What: what, ByHand: byHand, Exists: c.State != gitbackend.CloneMissing, Collapsed: true}}
	if c.State == gitbackend.CloneMissing {
		return out
	}
	out = append(out, whereStateDir(c.Paths.StateDir, opswhere.AreaStore, "lore's own files for this clone: never committed, never in the files you browse")...)
	attrs := filepath.Join(c.Paths.GitDir, "info", "attributes")
	if fileHas(attrs, whereRowAttribute) {
		out = append(out, opswhere.Entry{Area: opswhere.AreaStore, Path: attrs, Kind: opswhere.KindBlock, Exists: true,
			What:   "a line telling git not to merge row files line by line (lore merges them field by field)",
			ByHand: "removing it lets a plain git pull leave conflict markers in row files; lore adds it again"})
	}
	out = append(out, whereGitConfig(ctx, c.Paths.Dir, filepath.Join(c.Paths.GitDir, "config"), opswhere.AreaStore)...)
	return out
}

// whereStateDir lists a lore state folder (.git/lore in a clone or code
// repo, .lore-state in a folder store): the folder, then each file it
// holds by name.
func whereStateDir(dir, area, what string) []opswhere.Entry {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := []opswhere.Entry{{Area: area, Path: dir, Kind: opswhere.KindFolder, Exists: true,
		What: what, ByHand: "do not edit; files below say what each one holds"}}
	for _, e := range ents {
		what := "lore's state for this clone"
		for _, w := range whereGitLore {
			if ok, _ := filepath.Match(w.pattern, e.Name()); ok {
				what = w.what
				break
			}
		}
		out = append(out, opswhere.Entry{Area: area, Path: filepath.Join(dir, e.Name()), Kind: whereKind(e.IsDir()), What: what, Exists: true, Collapsed: e.IsDir()})
	}
	return out
}

// whereGitConfig lists the config keys lore sets in the repository at
// dir (its config file is cfg): the merge drivers, the client-repo mark,
// fetch refspecs for lore's refs.
func whereGitConfig(ctx context.Context, dir, cfg, area string) []opswhere.Entry {
	keys := `^(merge\.lore|lore\.|remote\..*\.fetch)`
	if area == opswhere.AreaStore {
		// a store clone's identity is lore's too (storeidentity.go); a
		// code repo's user.* is the person's and is never listed
		keys = `^(merge\.lore|lore\.|remote\..*\.fetch|user\.(name|email))$`
	}
	b, err := gitRun{dir: dir, args: []string{"config", "--local", "--get-regexp", keys}}.output(ctx)
	if err != nil {
		return nil
	}
	var out []opswhere.Entry
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		key, val, _ := strings.Cut(line, " ")
		var what string
		switch {
		case strings.HasPrefix(key, "merge.lore"):
			what = "the merge driver git runs for lore's row files (lore merges them field by field)"
		case key == ClientRepoGitKey:
			what = "marks this clone as a client repo, so its commit hook refuses when no lore can check a commit for internal notes"
		case strings.HasPrefix(key, "lore."):
			what = "a lore setting for this clone"
		case key == gitbackend.ConfigUserName || key == gitbackend.ConfigUserEmail:
			what = "who your own git commits in this store clone are by (" + val + "): your identity for this store, set by lore when it cloned or first opened the clone (`lore doctor` checks it)"
			out = append(out, opswhere.Entry{Area: area, Path: cfg, Key: key, Kind: opswhere.KindGitConfig, What: what, Exists: true,
				ByHand: "you may change it (`git -C " + dir + " config " + key + " <value>`); lore doctor flags a clone whose identity is not your setup identity; removed, lore sets it again the next time it opens the clone"})
			continue
		case strings.Contains(val, "refs/lore"):
			what = "fetches lore's live refs with every git fetch"
		default:
			continue // a fetch refspec that is not lore's
		}
		out = append(out, opswhere.Entry{Area: area, Path: cfg, Key: key, Kind: opswhere.KindGitConfig, What: what, Exists: true,
			ByHand: "removing it: lore sets it again the next time it writes here (`git config --unset " + key + "`)"})
	}
	return out
}

// fileHas reports whether the file at p contains s.
func fileHas(p, s string) bool {
	b, err := os.ReadFile(filepath.Clean(p))
	return err == nil && strings.Contains(string(b), s)
}

// whereFormat is the project format as lore where names it (D12).
func whereFormat(top string) string {
	pf, err := ProjectFormatAt(top)
	if err != nil {
		return "unreadable: " + errLine(err)
	}
	return ProjectFormatLine(pf)
}

// whereRepo lists what lore keeps in the code repo at top.
func whereRepo(ctx context.Context, top string) []opswhere.Entry {
	var out []opswhere.Entry
	add := func(p, kind, what, byHand string) {
		if fi, err := os.Lstat(p); err == nil {
			// a folder lore owns whole stands for what is in it (the rows,
			// the local state, rendered rules); .lore/ itself is walked
			collapsed := fi.IsDir() && p != filepath.Join(top, rowfs.LoreDir)
			out = append(out, opswhere.Entry{Area: opswhere.AreaRepo, Path: p, Kind: kind, What: what, ByHand: byHand, Exists: true, Collapsed: collapsed})
		}
	}
	lore := filepath.Join(top, rowfs.LoreDir)
	add(lore, opswhere.KindFolder, "lore's folder in this repo", "the files below say which are committed")
	add(filepath.Join(top, filepath.FromSlash(rowfs.LayoutRepo.DataPath(rowfs.FolderRoot))), opswhere.KindFolder,
		"this repo's rows, one JSON file per row ("+filepath.Join(filepath.FromSlash(rowfs.LayoutRepo.DataPath(rowfs.FolderRoot)), "<table>", "<id>"+rowfs.RowFileExt)+") and "+rowfs.MetaFileName+" (the project id and format: "+whereFormat(top)+"); committed",
		"rows may be edited with any editor; deleting "+rowfs.MetaFileName+" gives the repo a new project id")
	for _, f := range []string{RepoCacheFile, RepoCacheFile + "-wal", RepoCacheFile + "-shm"} {
		add(filepath.Join(top, filepath.FromSlash(f)), opswhere.KindFile, "an earlier lore build's search cache of this repo (the cache now lives under the lore home, cache/"+repoCacheDir+"/); untracked and rebuildable", "safe to delete; `lore fix` removes it")
	}
	// B4-7: what lore 0.1.17 kept in the repo (untracked or ignored): this
	// lore reads lore.db only to migrate the edits only it held
	for _, f := range []string{v017LoreDB, v017LoreDB + "-wal", v017LoreDB + "-shm"} {
		add(filepath.Join(top, filepath.FromSlash(f)), opswhere.KindFile, "lore 0.1.17's database of this checkout; `lore fix` (migrate) copies the edits only it held into .lore/data, then nothing reads it; untracked", "`lore fix` removes it once every row it holds is in a store here")
	}
	add(filepath.Join(lore, "backups"), opswhere.KindFolder, "backups lore 0.1.17 made of this checkout's database; this lore does not read them; untracked", "`lore fix` removes them with lore.db")
	add(filepath.Join(lore, rowfs.LockFileName), opswhere.KindFile, "the lock lore takes while it writes rows here; not committed", byHandLock)
	add(filepath.Join(lore, "state"), opswhere.KindFolder, "local state of this checkout (the lock's audit journal, a migration's journal); not committed", "do not delete while a migration is half done")
	add(filepath.Join(lore, "stores.yaml"), opswhere.KindFile, "the stores this repo attaches (committed)", "you may edit it; `lore store add|remove` does it for you")
	if ents, err := os.ReadDir(lore); err == nil {
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				add(filepath.Join(lore, e.Name()), opswhere.KindFile, "context lore renders from the rows for coding agents (committed)", "edits are overwritten by the next `lore render`")
			}
		}
	}
	for _, p := range toolprofile.Builtins() {
		if p.PointerFile != "" {
			if f := filepath.Join(top, filepath.FromSlash(p.PointerFile)); fileHas(f, wherePointerStart) || fileHas(f, whereContextStart) {
				add(f, opswhere.KindBlock, "lore's marked block pointing "+p.Name+" at .lore/LORE.md; the rest of the file is yours", "text outside the block is never touched; lore adds the block again on render")
			}
		}
		if p.PrivateFile != "" {
			if f := filepath.Join(top, filepath.FromSlash(p.PrivateFile)); fileHas(f, "lore") {
				add(f, opswhere.KindBlock, "lore's private import for "+p.Name+" (context from internal stores, under lore's home); kept out of git", "lore adds it again on render")
			}
		}
		if p.PathRules.Dir != "" {
			add(filepath.Join(top, filepath.FromSlash(p.PathRules.Dir)), opswhere.KindFolder, "rules lore renders for "+p.Name+" per path", "edits are overwritten by the next `lore render`")
		}
	}
	for _, h := range toolprofile.SessionHooks() {
		if f := filepath.Join(top, filepath.FromSlash(h.ConfigFile)); fileHas(f, whereHookWord) {
			add(f, opswhere.KindBlock, "the agent hook entries lore added for "+h.Tool+" (`lore setup --agent-hooks`)", "removing them stops the hooks; other settings are yours")
		}
	}
	if f := filepath.Join(top, ".gitignore"); fileHas(f, whereIgnoreHeader) {
		add(f, opswhere.KindBlock, "lore's lines keeping its cache, lock and state out of git, and the rows in", "removed lines come back with the next lore write here")
	}
	if f := filepath.Join(top, ".gitattributes"); fileHas(f, whereGitBlock) {
		add(f, opswhere.KindBlock, "lore's block: row files merge with lore's merge driver and show collapsed in diffs", "lore adds it again with the next write here")
	}
	gitDir := whereGitDir(ctx, top)
	if gitDir == "" {
		return out
	}
	if f := filepath.Join(gitDir, "info", "exclude"); fileHas(f, whereExcludeWord) {
		add(f, opswhere.KindBlock, "lore's lines keeping its local files out of git status in this clone only", "lore adds them again; `lore doctor --fix` moves them to .gitignore")
	}
	out = append(out, whereGitConfig(ctx, top, filepath.Join(gitDir, "config"), opswhere.AreaRepo)...)
	out = append(out, whereHooks(ctx, top)...)
	out = append(out, whereStateDir(filepath.Join(gitDir, "lore"), opswhere.AreaRepo, "lore's own files for this repo: never committed")...)
	out = append(out, whereRefs(ctx, top, gitDir)...)
	return out
}

// whereGitDir is the repository's git folder ("" when git cannot say).
func whereGitDir(ctx context.Context, top string) string {
	b, err := gitRun{dir: top, args: []string{"rev-parse", "--absolute-git-dir"}}.output(ctx)
	if err != nil {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(b))))
}

// whereHooks lists the hook files holding lore's blocks (in the folder
// git runs hooks from).
func whereHooks(ctx context.Context, top string) []opswhere.Entry {
	b, err := gitRun{dir: top, args: []string{"rev-parse", "--path-format=absolute", "--git-path", "hooks"}}.output(ctx)
	if err != nil {
		return nil
	}
	dir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(b))))
	if hp, herr := (gitRun{dir: top, args: []string{"config", "--get", "core.hooksPath"}}).output(ctx); herr == nil {
		if p := filepath.FromSlash(strings.TrimSpace(string(hp))); p != "" {
			if !filepath.IsAbs(p) {
				p = filepath.Join(top, p)
			}
			dir = p
		}
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []opswhere.Entry
	for _, e := range ents {
		f := filepath.Join(dir, e.Name())
		if e.IsDir() || !fileHas(f, whereGitBlock) {
			continue
		}
		out = append(out, opswhere.Entry{Area: opswhere.AreaRepo, Path: f, Kind: opswhere.KindBlock, Exists: true,
			What:   "lore's marked block in the " + e.Name() + " hook (checks rows and secrets on commit, keeps the live ref in step)",
			ByHand: "text outside the block is yours; a removed block comes back with the next lore write here"})
	}
	return out
}

// whereRefs lists lore's refs, one entry per namespace with a count.
func whereRefs(ctx context.Context, top, gitDir string) []opswhere.Entry {
	var out []opswhere.Entry
	for _, r := range whereRefPrefixes {
		b, err := gitRun{dir: top, args: []string{"for-each-ref", "--format=%(refname)", r.prefix}}.output(ctx)
		if err != nil {
			continue
		}
		refs := strings.Fields(string(b))
		if len(refs) == 0 {
			continue
		}
		sort.Strings(refs)
		key := r.prefix + "*"
		if len(refs) == 1 {
			key = refs[0]
		}
		out = append(out, opswhere.Entry{Area: opswhere.AreaRepo, Path: gitDir, Key: key, Kind: opswhere.KindGitRef, Exists: true,
			What:   r.what + " (" + strconv.Itoa(len(refs)) + " ref(s))",
			ByHand: "do not delete refs/lore/live while it holds writes not landed; backups are safe to delete"})
	}
	return out
}

// whereUser lists the skill folders that exist and the lore binary.
func whereUser() []opswhere.Entry {
	var out []opswhere.Entry
	if uh, err := os.UserHomeDir(); err == nil {
		for _, p := range toolprofile.Builtins() {
			if dir := p.SkillPath(uh); dir != "" {
				if _, serr := os.Stat(dir); serr == nil {
					out = append(out, opswhere.Entry{Area: opswhere.AreaUser, Path: dir, Kind: opswhere.KindFolder, Exists: true,
						What: "the lore skill " + p.Name + " loads", ByHand: "deleting it removes the skill; `lore skill install` puts it back"})
				}
			}
		}
	}
	if exe, err := os.Executable(); err == nil {
		out = append(out, opswhere.Entry{Area: opswhere.AreaUser, Path: exe, Kind: opswhere.KindFile, Exists: true,
			What: "the lore program itself", ByHand: "`lore upgrade` replaces it"})
	}
	return out
}
