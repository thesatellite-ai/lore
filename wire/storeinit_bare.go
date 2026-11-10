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
	"path/filepath"

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

// refuseBareRepoFolder is E_INVALID_INPUT when the folder of a file:// store
// url is a bare git repository; nil otherwise (and for every other folder).
func refuseBareRepoFolder(name, dir string) error {
	if !isBareGitRepo(dir) {
		return nil
	}
	what := "store init " + name
	if name == "" {
		name, what = "<name>", "store" // a store made by a setup step or a team: no store name typed
	}
	return errs.New(errs.InvalidInput, what+": "+dir+" is a git repository, and a file:// path is always a plain folder store; nothing written").
		WithHint("this is a git repository: attach it with its git url as a git store, e.g. lore store init " + name + " ssh://<host>" + filepath.ToSlash(dir) + " or git@github.com:<owner>/<repo>; to keep notes in a plain folder, give a folder that is not a git repository")
}
