package rowfs

// storefiles.go — the one definition of "a file a lore store may hold"
// (LORE_TEAM_SPEC E10, F12; item 32b). Every check that asks whether a
// tree is a store's — the org links reader's check of a store branch
// (orgcfg.CheckStoreTree), the adopt check of a repo without a marker
// (git backend foreignFiles) — asks StoreFileAllowed, so a file lore
// itself writes into a store can never make lore refuse that store.
// The property test wire TestEveryStoreWriteKeepsALoreStore drives every
// store writer and checks each path it leaves against this function.

import (
	"path"
	"slices"
	"strings"
)

// Org config names (D-e): the org file of an org folder, and the copy of
// it `lore migrate` keeps beside it and commits (org_defaults_to_policy).
const (
	OrgFileName     = "lore-org.yaml"
	OrgFileKeptName = OrgFileName + ".migrated"
)

// Files a store may hold besides lore's own (a git host's "new
// repository" starter files, lore's CI workflow), v0.1.17 stores.Allowed.
const (
	gitattributesName = ".gitattributes"
	gitignoreName     = ".gitignore"
	workflowDir       = ".github/workflows/"
	workflowPrefix    = "lore-"
	workflowExt       = ".yml"
)

// starterStems are the top-level files a git host offers to create with a
// new repository, by lower-cased base name before the first dot
// (README.md, LICENSE.txt, COPYING …).
var starterStems = []string{"readme", "license", "licence", "copying"}

// IsStarterFile reports whether p (slash path from the store's top) is a
// git host's starter file at the top: README*, LICENSE*, LICENCE*,
// COPYING*, .gitignore, .gitattributes (case-insensitive).
func IsStarterFile(p string) bool {
	if strings.Contains(p, "/") {
		return false
	}
	low := strings.ToLower(p)
	if low == gitignoreName || low == gitattributesName {
		return true
	}
	stem, _, _ := strings.Cut(low, ".")
	return slices.Contains(starterStems, stem)
}

// IsOrgFileName reports whether a base name is the org file or the copy of
// it `lore migrate` keeps.
func IsOrgFileName(name string) bool { return name == OrgFileName || name == OrgFileKeptName }

// StoreFileAllowed reports whether one path (slash separated, from the
// store's top, as git ls-tree prints it) may exist in a lore store:
//
//	lore-store.yaml                       the marker
//	README*, LICENSE*, .gitignore, …      a git host's starter files (IsStarterFile)
//	.github/workflows/lore-*.yml          lore's CI workflow
//	lore-org.yaml[.migrated]              the org file when org_folder is "."
//	_store/<table>/<id>.json              the store's own rows (D-ad)
//	<folder>/<table>/<id>.json            a folder's rows
//	<table>/<id>.json                     the root's rows of the first plain layout (lore_store: 3)
//	<folder>/_meta.json, _purged.json     a folder's own files (also _store/, the top)
//	<folder>/lore-org.yaml[.migrated]     an org folder's org file
//	.lore/…, <folder>/.lore/…             the layout before plain files
//
// A folder is any name CleanFolder accepts; a table any table lore
// knows. Anything else (a code file, a stray note) is not a store's.
// ds:def id=team-store-allowed-a7qb5uuj owner=@khanakia stability=stable desc="files a lore store may hold"
func StoreFileAllowed(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return false
	}
	if p == MarkerFile {
		return true
	}
	if !strings.Contains(p, "/") {
		return IsStarterFile(p) || IsOrgFileName(p) || p == MetaFileName || p == PurgedFileName
	}
	if rest, ok := strings.CutPrefix(p, workflowDir); ok {
		return !strings.Contains(rest, "/") && strings.HasPrefix(rest, workflowPrefix) && strings.HasSuffix(rest, workflowExt)
	}
	if strings.HasPrefix(p, LoreDir+"/") {
		return true
	}
	if _, _, ok := LayoutStore.SplitRowPath(p); ok {
		return true
	}
	if _, _, ok := ParseRowPath(p); ok {
		return true // the root folder's row at the store's top: the first plain layout (lore_store: 3), moved into _store/ by the next lore
	}
	if _, _, ok := LayoutStore.SplitFolderFile(p); ok {
		return true
	}
	if dir, name := path.Split(p); IsOrgFileName(name) {
		if _, err := CleanFolder(strings.TrimSuffix(dir, "/")); err == nil {
			return true
		}
	}
	// <folder>/.lore/…: the old layout, a folder of any depth (folder
	// names of that time could still be table names, so only the segment
	// shape is checked)
	if i := strings.Index(p, "/"+LoreDir+"/"); i > 0 {
		for seg := range strings.SplitSeq(p[:i], "/") {
			if !folderSegment.MatchString(seg) {
				return false
			}
		}
		return true
	}
	return false
}
