package wire

// homeguard.go — a member join that is refused or fails leaves the lore
// home as it was (devco D-644, owner use pass 3 wrong 6). A join by store
// url runs org import, which records the alias access rule and the org
// entry in config.yaml, clones the store under stores/; a refusal by the roster took the entry back out but kept
// the rest, and a failure after the import kept everything: the setup was
// "not saved yet, next: join" with an org entry, a clone and an access
// rule on disk. The join step's check (a read) left the same behind.
//
//	guardHome(home) ─▶ the bytes of config.yaml and
//	                   state/store-clones.json (nil when absent) and the
//	                   store clone folders stores/<owner>/<repo> there now
//	… org import, roster check, join …
//	refused or failed ─▶ undo: each file back to its bytes (removed when
//	                     it was absent), each clone folder made since
//	                     removed (and its owner folder when left empty)

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/orgcmd"
)

// guardedHomeFiles are the lore-home files a join's org import may write,
// relative to the home. machine.json (this machine's id) is not one: any
// command may make it, and the setup's progress row is written with it.
var guardedHomeFiles = []string{"config.yaml", filepath.Join("state", "store-clones.json")}

// storesDir is the folder of the store clones under the lore home.
const storesDir = "stores"

// homeGuard is what a join may change in the lore home, as it was before.
type homeGuard struct {
	home string
	// files maps each guarded file to its bytes, nil when it was absent.
	files map[string][]byte
	// clones is every stores/<owner>/<repo> folder there was.
	clones map[string]bool
}

// guardHome records home's guarded files and store clone folders.
func guardHome(home string) homeGuard {
	g := homeGuard{home: home, files: map[string][]byte{}, clones: map[string]bool{}}
	if home == "" {
		return g
	}
	for _, f := range guardedHomeFiles {
		b, err := os.ReadFile(filepath.Join(home, f)) //nolint:gosec // a file of this machine's lore home
		if err != nil {
			b = nil
		} else if b == nil {
			b = []byte{}
		}
		g.files[f] = b
	}
	for _, c := range cloneFolders(home) {
		g.clones[c] = true
	}
	return g
}

// cloneFolders is every stores/<owner>/<repo> folder under home (relative
// paths), besides the personal store's _me and other top-level stores.
func cloneFolders(home string) []string {
	var out []string
	owners, _ := os.ReadDir(filepath.Join(home, storesDir))
	for _, o := range owners {
		if !o.IsDir() {
			continue
		}
		out = append(out, filepath.Join(storesDir, o.Name()))
		repos, _ := os.ReadDir(filepath.Join(home, storesDir, o.Name()))
		for _, r := range repos {
			if r.IsDir() {
				out = append(out, filepath.Join(storesDir, o.Name(), r.Name()))
			}
		}
	}
	return out
}

// undo puts the guarded files back and removes the clone folders made
// since guardHome; every failure is joined into the error returned.
func (g homeGuard) undo() error {
	if g.home == "" {
		return nil
	}
	var errList []error
	for f, b := range g.files {
		p := filepath.Join(g.home, f)
		if b == nil {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errList = append(errList, err)
			}
			continue
		}
		if cur, err := os.ReadFile(p); err == nil && string(cur) == string(b) { //nolint:gosec // a file of this machine's lore home
			continue
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			errList = append(errList, err)
		}
	}
	// deepest first: a new clone, then its owner folder if it is new too
	made := cloneFolders(g.home)
	for i := len(made) - 1; i >= 0; i-- {
		if g.clones[made[i]] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(g.home, made[i])); err != nil {
			errList = append(errList, err)
		}
	}
	return errors.Join(errList...)
}

// failed is err after undo: the home back as it was, or err joined with
// what could not be put back (the person then sees both).
func (g homeGuard) failed(err error) error {
	if err == nil {
		return nil
	}
	if uerr := g.undo(); uerr != nil {
		return errors.Join(err, errs.Wrap(errs.Internal, "the join's changes to "+g.home+" could not all be taken back", uerr))
	}
	return err
}

// probeImportMu serialises the taken-back imports of the setup checks and
// prefills: one guard's undo must never remove a clone another import is
// reading, and a guard taken while another import runs would keep its clone.
var probeImportMu sync.Mutex

// probeImport is org import for a setup read (a step's check, the join
// plan's roster prefill): it runs in, Admit's errProbeAdmitted is a yes
// (nil), and the lore home is always put back as it was, whatever the
// roster said (devco D-655: the join plan's prefill took the entry back
// but kept config.yaml and the store clone, so a plan never applied left
// the home changed). Only apply keeps what an import writes.
func probeImport(ctx context.Context, host orgcmd.Host, home string, in orgcmd.ImportIn) error {
	probeImportMu.Lock()
	defer probeImportMu.Unlock()
	guard := guardHome(home)
	_, err := host.Import(ctx, in, true)
	if errors.Is(err, errProbeAdmitted) {
		err = nil
	}
	if uerr := guard.undo(); uerr != nil {
		return errors.Join(err, errs.Wrap(errs.Internal, "the setup read's changes to "+home+" could not all be taken back", uerr))
	}
	return err
}
