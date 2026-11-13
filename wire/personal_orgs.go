package wire

// personal_orgs.go — lore 0.1.17's per-org personal notes keep their scope
// (decision D5, upgrade journey B4-19). 0.1.17 filed a `--to me` note
// written in a repo of org acme under acme/ of the personal store, and
// loaded it only in acme's repos; everywhere/ loaded in every repo. In a
// git personal store this lore moves everywhere/ to _store/ (`me`, read in
// every repo; git.Backend.FlattenPersonal) and leaves <org>/ where it is.
// Here, in a repo whose org is acme, the personal store's acme/ folder is
// attached beside `me` as `me/acme` (read; `--to me/acme` writes there),
// so its notes load in acme's repos and nowhere else. `--store me` lists
// both (engine.NamedSources). A folder of an org this machine does not
// know is never attached: lore doctor names it (personalUnplacedFindings).

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/store"
)

// personalCloneDir is the clone folder of the git personal store at raw
// ("" when raw is not a git url or has no clone here).
func personalCloneDir(home, raw string) string {
	r, err := git.RemoteOfRaw(raw)
	if err != nil {
		return ""
	}
	dir, _, err := git.CloneDirFor(home, r, "")
	if err != nil || !isDirPath(filepath.Join(dir, ".git")) {
		return ""
	}
	return dir
}

// personalOrgStore is the `me/<org>` entry of the git personal store at
// raw for the org this repo resolves to, when its clone holds a folder for
// that org; ok is false otherwise.
func personalOrgStore(ctx context.Context, opts Options, raw string) (attach.Store, bool) {
	if opts.Org == nil {
		return attach.Store{}, false
	}
	org, err := opts.Org(ctx)
	if err != nil || org == nil {
		return attach.Store{}, false
	}
	f, err := rowfs.CleanFolder(org.Name)
	if err != nil || f == rowfs.FolderRoot || strings.HasPrefix(f, "_") {
		return attach.Store{}, false
	}
	dir := personalCloneDir(opts.Home, raw)
	if dir == "" || !rowfs.HasStoreFolder(dir, f) {
		return attach.Store{}, false
	}
	return attach.Store{Name: store.Source(attach.ReservedMe + targetSep + f), URL: raw, Folder: f}, true
}

// personalUnplacedFindings names the folders of a git personal store that
// lore 0.1.17 made for an org this machine has no entry for: their notes
// load nowhere until the org is set up here (or the notes are moved).
func (a *App) personalUnplacedFindings() []DoctorFinding {
	known := map[string]bool{}
	if cfg, _, err := orgcfg.LoadConfig(a.opts.Home); err == nil {
		for _, o := range cfg.Orgs {
			known[o.Name] = true
		}
	}
	var out []DoctorFinding
	for _, raw := range personalStoreURLs(a.opts.Home) {
		dir := personalCloneDir(a.opts.Home, raw)
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			f := e.Name()
			if !e.IsDir() || f == personalEverywhereV017 || strings.HasPrefix(f, "_") || strings.HasPrefix(f, ".") || known[f] {
				continue
			}
			if cf, cerr := rowfs.CleanFolder(f); cerr != nil || cf == rowfs.FolderRoot || !rowfs.HasStoreFolder(dir, f) {
				continue
			}
			if !folderHasRows(filepath.Join(dir, f)) {
				continue
			}
			out = append(out, DoctorFinding{Check: DoctorCheckOldClones, Level: DoctorWarning,
				Message: "personal store folder " + filepath.Join(dir, f) + " holds notes lore 0.1.17 filed for org " + f + ", which this machine has no entry for, so they load in no repo",
				Fix:     "set org " + f + " up on this machine (its repos then read them), or move each note: lore move --id <id> --to me"})
		}
	}
	return out
}

// folderHasRows reports whether any row file sits under p (either layout).
func folderHasRows(p string) bool {
	found := false
	_ = filepath.WalkDir(p, func(q string, d os.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		if !d.IsDir() && filepath.Ext(q) == ".json" {
			if _, _, ok := rowfs.ParseRowPath(filepath.Base(filepath.Dir(q)) + "/" + filepath.Base(q)); ok {
				found = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found
}

// personalEverywhereStore is the `me/everywhere` entry (read; its writes
// are refused by storeFormatGuard until lore migrate run) of the git
// personal store at raw while its clone is on lore 0.1.17's layout and
// still holds everywhere/ (D16: the move to _store/ is lore migrate run's).
func personalEverywhereStore(home, raw string) (attach.Store, bool) {
	dir := personalCloneDir(home, raw)
	if dir == "" || !git.PersonalEverywhereV017At(dir) {
		return attach.Store{}, false
	}
	return attach.Store{Name: store.Source(attach.ReservedMe + targetSep + personalEverywhereV017), URL: raw, Folder: personalEverywhereV017}, true
}
