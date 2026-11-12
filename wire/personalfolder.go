package wire

// personalfolder.go — where the personal store sits when it is a plain
// folder (owner item 17, 2026-11-12).
//
//	~/.lore/stores/<owner>/<repo>/   a git clone (the personal store too,
//	                                 when it is a git store)
//	~/.lore/stores/_me/              the personal store as a plain folder
//
// Before, the folder was stores/me, beside the owner folders of clones:
// a git owner literally named "me" (stores/me/<repo>) clashed with it.
// GitHub and GitLab owner names cannot start with "_", so stores/_me
// never clashes, and "_" marks lore's own, as _store/ does in a store.
//
// movePersonalFolder moves an existing stores/me there on the first run
// after the upgrade: only when stores/me is lore's folder store (its
// lore-store.yaml marker, no .git) AND config.yaml names it as a personal
// store (personal.store or a setup's personal_store). Under the
// config.yaml lock it renames the folder, points those config places at
// stores/_me and prints one line. A second run finds nothing to move; a
// git clone at stores/me/<repo> (owner "me") is never touched.

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// personalFolderDir is the local personal store's folder under home.
func personalFolderDir(home string) string {
	return filepath.Join(home, personalStoresDir, onboard.LocalStoreFolder)
}

// legacyPersonalFolderDir is that folder before 2026-11-12.
func legacyPersonalFolderDir(home string) string {
	return filepath.Join(home, personalStoresDir, onboard.LegacyLocalStoreFolder)
}

// isFolderStore reports whether dir is a plain-folder store: a marker and
// no .git (a git clone carries a marker too).
func isFolderStore(dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, rowfs.MarkerFile)); err != nil || !info.Mode().IsRegular() {
		return false
	}
	_, err := os.Lstat(filepath.Join(dir, gitDirName))
	return os.IsNotExist(err)
}

// movePersonalFolder moves the legacy personal folder store to
// personalFolderDir (see the file comment) and prints one line to w when
// it did. Best effort: a move that fails leaves everything as it was and
// is tried again by the next command; the error is printed, never
// returned, so no command fails over it.
func movePersonalFolder(home string, w io.Writer) {
	moved, err := movePersonalFolderLocked(home)
	switch {
	case err != nil:
		_, _ = fmt.Fprintln(w, "lore: could not move your personal store folder "+legacyPersonalFolderDir(home)+" to "+personalFolderDir(home)+" (tried again next run): "+err.Error())
	case moved:
		_, _ = fmt.Fprintln(w, "lore: moved your personal store folder "+legacyPersonalFolderDir(home)+" to "+personalFolderDir(home)+" (so it never clashes with a git owner named me); ~/.lore/"+ConfigFile+" now names the new place")
	}
}

// movePersonalFolderLocked does the move; moved says it happened.
func movePersonalFolderLocked(home string) (moved bool, err error) {
	if home == "" {
		return false, nil
	}
	old, dst := legacyPersonalFolderDir(home), personalFolderDir(home)
	// The quick check every command pays: one stat when there is no
	// legacy folder store.
	if !isFolderStore(old) {
		return false, nil
	}
	edit, hit := personalFolderEdit(old, fileuri.URL(dst, ""))
	// Checked before the lock: a config naming another personal store
	// takes no lock, every run. A config that does not read or validate
	// is left to the command itself (and `lore config check`) to report.
	if _, cerr := orgConfigWith(home, edit); cerr != nil || !*hit {
		return false, nil
	}
	release, err := lockMachineConfig(home)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, release()) }()
	// Again under the lock: another lore process may have moved it.
	if !isFolderStore(old) {
		return false, nil
	}
	if _, serr := os.Lstat(dst); serr == nil {
		return false, errs.New(errs.InvalidInput, dst+" already exists; move or merge one of the two folders by hand")
	}
	b, err := orgConfigWith(home, edit)
	if err != nil || !*hit {
		return false, err
	}
	if err = os.Rename(old, dst); err != nil {
		return false, errs.FileIO("rename", old, err)
	}
	if err = writeMachineConfigFile(configPath(home), b, configFileMode); err != nil {
		if rerr := os.Rename(dst, old); rerr != nil {
			return false, errors.Join(err, errs.FileIO("rename back", dst, rerr))
		}
		return false, err
	}
	return true, nil
}

// personalFolderEdit is the config.yaml edit pointing every personal
// store place (personal.store, setups[].personal_store) that names the
// folder old at newURL; *hit says the last run of it changed one.
func personalFolderEdit(old, newURL string) (func(root *yaml.Node) error, *bool) {
	hit := new(bool)
	return func(root *yaml.Node) error {
		*hit = false
		if p := mapGet(root, configKeyPersonal); p != nil {
			if s := mapGet(p, configKeyStore); s != nil && fileURLIs(s.Value, old) {
				mapSet(p, configKeyStore, scalar(newURL))
				*hit = true
			}
		}
		if setups := mapGet(root, configKeySetups); setups != nil && setups.Kind == yaml.SequenceNode {
			for _, item := range setups.Content {
				if item.Kind != yaml.MappingNode {
					continue
				}
				if ps := mapGet(item, configKeyPersonalStore); ps != nil && fileURLIs(ps.Value, old) {
					mapSet(item, configKeyPersonalStore, scalar(newURL))
					*hit = true
				}
			}
		}
		return nil
	}, hit
}

// fileURLIs reports whether raw is a file url naming dir (a ~ in it read
// as the user's home, as cleanFileURL reads it).
func fileURLIs(raw, dir string) bool {
	clean, err := cleanFileURL(raw)
	if err != nil {
		return false
	}
	u, err := url.Parse(clean)
	if err != nil || u.Scheme != fileuri.Scheme {
		return false
	}
	return filepath.Clean(fileuri.OSPath(u.Path)) == filepath.Clean(dir)
}
