package wire

// storeinit_bare.go — `lore store init <name> file:///…` of a bare git
// repository. A file:// url is always a folder store (wire/open.go
// canonical: only ssh, https and http urls open the git backend), so
// before this check the init wrote lore-store.yaml and .lore/data into the
// bare repository's own folder, beside HEAD and objects/, where git never
// sees them and no teammate ever receives them. The person meant a git
// store: refuse and name the spelling that attaches one.

import (
	"os"
	"path"
	"path/filepath"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
)

// bareRepoMarkers are what every bare git repository holds at its top
// (git's own layout: the HEAD file and the objects and refs folders). A
// folder holding all three is a git repository, not a folder for lore.
var bareRepoMarkers = []struct {
	name string
	dir  bool
}{{"HEAD", false}, {"objects", true}, {"refs", true}}

// isBareGitRepo reports whether dir is a bare git repository. A missing
// folder or one lacking any marker is not (store init creates or uses it
// as a folder store, as before).
func isBareGitRepo(dir string) bool {
	for _, m := range bareRepoMarkers {
		st, err := os.Stat(filepath.Join(dir, m.name))
		if err != nil || st.IsDir() != m.dir {
			return false
		}
	}
	return true
}

// localGitHost is the host name the hint gives a local bare repository:
// git rewrites it to the folder (url.<folder>.insteadOf), so the store has
// a git url like any other. ".invalid"-style names are not used because
// lore checks a store host's form, and this one only ever reaches git.
const localGitHost = "lore-local"

// refuseBareRepoFolder is E_INVALID_INPUT when the folder of a file:// store
// url is a bare git repository; nil otherwise (and for every other folder).
//
// Why not accept it as a git store: lore reads a store's kind from its url
// alone, so the url means the same on every machine and in lore-org.yaml;
// file:// always opens a plain folder store, and a bare repository holds
// git's objects, not row files, so lore would write its files beside them
// where no clone ever sees them. The hint gives the git url spelling that
// works for a local bare repository (a host alias git maps to the folder,
// as `git clone` itself would reach it) with the real path, which the
// output never redacts (logic/sessionmask: plain local paths are shown).
func refuseBareRepoFolder(name, dir string) error {
	if !isBareGitRepo(dir) {
		return nil
	}
	what := "store init " + name
	if name == "" {
		name, what = "<name>", "store" // a store made by a setup step or a team: no store name typed
	}
	slash := filepath.ToSlash(dir)
	alias := "ssh://" + localGitHost + "/" + path.Base(slash)
	return errs.New(errs.InvalidInput, what+": "+dir+" is a git repository (bare), and a file:// url always opens a plain folder store (lore reads a store's kind from its url, the same on every machine), so its rows would sit beside git's objects where no clone sees them; nothing written").
		WithHint("attach it as a git store by a git url: a remote your team reaches (git@github.com:<owner>/<repo>.git), or for this machine only map a host name to the folder and use that url: " +
			"git config --global url.\"" + fileuri.URL(dir, "") + "\".insteadOf " + alias + " && lore store init " + name + " " + alias + "; to keep notes in a plain folder instead, give a folder that is not a git repository")
}
