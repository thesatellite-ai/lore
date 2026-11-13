package host

// repo_setup.go — S-2, the untouched-repo rule: a git repo with no lore
// data stays byte-identical (its tree, .git/config, .git/hooks,
// .git/info, .git/lore) after every command but the few that set a repo up
// on purpose. Runner.Run gives every other command wire.WithNoSetUp, so
// its open refuses in a repo with no .lore/ ("lore isn't set up in …;
// lore init sets this repo up") before a folder, cache, live lock, hook
// or merge driver is made. The rule is read from this one list, so a new
// command is covered the day it is added (app's
// TestUntouchedRepoEveryCommand walks the whole command tree).

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	opslink "github.com/thesatellite-ai/lore/ops/link"
	opsorg "github.com/thesatellite-ai/lore/ops/orgcmd"
	opsrepoinit "github.com/thesatellite-ai/lore/ops/repoinit"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// setUpPrefix starts every setup operation's name: lore setup's steps set
// repos up (the This folder step runs lore init).
const setUpPrefix = "setup."

// repoSetUpOps are the operations that set the repo they run in up on
// purpose, and so may create .lore/ there:
//   - repo.init: lore init (`lore repo init`, the setup page's step);
//   - org.link / org.unlink, link.add / link.remove: a link names the repo
//     in a store folder (a repo only a link names still gets no .lore/: its
//     open is read-only, wire/open.go);
//   - store.init / store.add / store.adopt: make or attach a store and
//     attach it to this repo (.lore/stores.yaml);
//   - sync.run: `lore sync` writes the project's row file of a clone whose
//     rows so far live only on the live ref (the hint of E_NOT_FOUND "no
//     lore project" names it).
var repoSetUpOps = map[string]bool{
	opsrepoinit.OpRepoInit: true,
	opsorg.OpLink:          true,
	opsorg.OpUnlink:        true,
	opslink.OpAdd:          true,
	opslink.OpRemove:       true,
	opsstores.OpInit:       true,
	opsstores.OpAdd:        true,
	opsstores.OpAdopt:      true,
	opssync.OpRun:          true,
}

// setsUpRepo reports whether the operation named name may set the repo it
// runs in up (repoSetUpOps, or any setup operation).
func setsUpRepo(name string) bool {
	return repoSetUpOps[name] || strings.HasPrefix(name, setUpPrefix)
}

// freshRepo is a git repo with no lore data before a set-up command ran
// in it: which of lore's two folders were absent then. A set-up command
// that ends refused (a store url with no scheme, a link to a folder that
// does not exist) then leaves the repo as untouched as it found it: the
// .lore/ folders it made and left empty go again, and so does the .git/lore
// state folder (its live lock, its notes), which only this run made.
type freshRepo struct {
	lore     string // <top>/.lore, absent before the run ("" when it existed)
	gitState string // <git common dir>/lore, absent before the run ("" when it existed)
}

// takeFreshRepo records the absent folders of the repo dir is in, for a
// set-up command only (every other command refuses before it makes
// anything there, WithNoSetUp); nil when there is nothing to protect.
func takeFreshRepo(ctx context.Context, name, dir string) *freshRepo {
	if !setsUpRepo(name) {
		return nil
	}
	out, err := gitIn(ctx, dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		return nil
	}
	f := &freshRepo{}
	if p := filepath.Join(lines[0], loreDirName); !exists(p) {
		f.lore = p
	}
	if p := filepath.Join(lines[1], liveStateDirName); !exists(p) {
		f.gitState = p
	}
	if f.lore == "" && f.gitState == "" {
		return nil
	}
	return f
}

// loreDirName is the project folder lore init makes at the repo top.
const loreDirName = ".lore"

// exists reports whether p is there (any kind of entry).
func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// restore removes what the refused run left of the absent folders: every
// empty folder of .lore/ (a file in it stays, and so do its folders), and
// the whole .git/lore state folder, which no one else writes.
func (f *freshRepo) restore() {
	if f == nil {
		return
	}
	if f.lore != "" {
		removeEmptyFolders(f.lore)
	}
	if f.gitState != "" {
		_ = os.RemoveAll(f.gitState)
	}
}

// removeEmptyFolders removes dir and every folder under it that holds
// nothing, deepest first.
func removeEmptyFolders(dir string) {
	var dirs []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	slices.Reverse(dirs)
	for _, d := range dirs {
		_ = os.Remove(d) // fails, as it should, on a folder that holds anything
	}
}
