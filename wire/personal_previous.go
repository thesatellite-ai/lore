package wire

// personal_previous.go — decision D10 (B4-23). Picking a personal store on
// the setup page (or `lore setup apply … --step personal_store`) that is
// not the one this machine used before must not hide that store's notes
// without a word. The step says how many notes the previous store holds
// and how to move them; and while the setup's store differs from the
// top-level personal.store, a previous store holding notes stays attached
// as `me-before` (never a default target: nothing is written there unless
// a command names it), so its notes keep showing, labelled, until they are
// moved with `lore move <id> --to me` (a move takes the row out of it,
// which is why it is not attached read-only).

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// PreviousPersonalStore names the attachment of the personal store a
// setup stopped using (D10).
const PreviousPersonalStore = "me-before"

// personalStoreDir is the folder holding the rows of the personal store at
// raw: a file:// url's folder, a git store's clone ("" when neither).
func personalStoreDir(home, raw string) string {
	if strings.HasPrefix(raw, fileuri.Scheme+"://") {
		if dir := fileuri.OSPath(strings.TrimPrefix(raw, fileuri.Scheme+"://")); isDirPath(dir) {
			return dir
		}
		return ""
	}
	return personalCloneDir(home, raw)
}

// noteTables are the tables a person's notes live in (the store's own
// rows, actors, machines and projects, are not notes).
func noteTable(t string) bool {
	switch t {
	case schema.TableActors, schema.TableMachines, schema.TableProjects, schema.TableRepos:
		return false
	}
	return true
}

// countNotes counts the live note rows under dir (either layout), up to
// limit (0: no limit).
func countNotes(dir string, limit int) int {
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == rowfs.StoreStateDir {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != ".json" {
			return nil
		}
		table := filepath.Base(filepath.Dir(p))
		if _, _, ok := rowfs.ParseRowPath(table + "/" + filepath.Base(p)); !ok || !noteTable(table) {
			return nil
		}
		if b, rerr := os.ReadFile(filepath.Clean(p)); rerr != nil || strings.Contains(string(b), `"_deleted": true`) {
			return nil
		}
		n++
		if limit > 0 && n >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	return n
}

// previousPersonalURL is the personal store a setup whose personal_store
// is value reads instead of the top-level one, when they differ: the
// top-level url ("" when the setup inherits it, has none, or they match).
func previousPersonalURL(home, value string) string {
	switch strings.TrimSpace(value) {
	case "", onboard.PersonalStoreInherit, onboard.PersonalStoreNone:
		return ""
	}
	mc, err := readMachineConfig(home)
	if err != nil {
		return ""
	}
	top := mc.sharedPersonalURL(home)
	if top == "" || sameStoreRaw(top, value) {
		return ""
	}
	return top
}

// sameStoreRaw reports two spellings of one store url.
func sameStoreRaw(a, b string) bool {
	ka, aok := storeKey(a)
	kb, bok := storeKey(b)
	if aok && bok {
		return ka == kb
	}
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// previousPersonalNote is the line the personal_store step prints when
// the new choice (value) leaves a store with notes behind ("" otherwise).
func (a *App) previousPersonalNote(_ context.Context, oldValue, value string) string {
	home := a.opts.Home
	old := ""
	switch strings.TrimSpace(oldValue) {
	case onboard.PersonalStoreNone:
	case "", onboard.PersonalStoreInherit:
		if mc, err := readMachineConfig(home); err == nil {
			old = mc.sharedPersonalURL(home)
		}
	default:
		old = oldValue
	}
	if old == "" || sameStoreRaw(old, value) {
		return ""
	}
	dir := personalStoreDir(home, old)
	if dir == "" {
		return ""
	}
	n := countNotes(dir, 0)
	if n == 0 {
		return ""
	}
	where := "it is not read by this setup any more; move each note with lore move --id <id> --to me --yes"
	if previousPersonalURL(home, value) != "" {
		where = "it stays readable here as store " + PreviousPersonalStore + " (labelled [" + PreviousPersonalStore + "]; nothing new is written there); move each note with lore move <id> --to me --yes (lore memory list --store " + PreviousPersonalStore + " lists them)"
	}
	return "setup apply personal_store: the personal store used before (" + old + ") holds " + strconv.Itoa(n) + " note(s) this setup's new store does not have; " + where
}

// previousPersonalStore is the `me-before` attachment for the
// setup's personal store value (D10), ok false when there is none.
func previousPersonalStore(home, value string) (attach.Store, bool) {
	prev := previousPersonalURL(home, value)
	if prev == "" {
		return attach.Store{}, false
	}
	dir := personalStoreDir(home, prev)
	if dir == "" || countNotes(dir, 1) == 0 {
		return attach.Store{}, false
	}
	return attach.Store{Name: store.Source(PreviousPersonalStore), URL: prev}, true
}

// setupPersonalValue is the active setup's own personal_store url ("" when
// it has none of its own: inherit, none, or no setup).
func setupPersonalValue(ctx context.Context, home, dir string) string {
	u, _, _ := setupPersonal(ctx, home, dir)
	return u
}
