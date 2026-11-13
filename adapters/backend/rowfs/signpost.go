package rowfs

// signpost.go — the one file a store holds for lore 0.1.17 (decision D11,
// bug B4-9). lore 0.1.17 cannot be told a store is newer: it checks only
// that every file of a store sits under a .lore folder and refuses any
// other with E_NOT_A_STORE "… is not a lore store (it holds non-lore files
// such as A, B, C and N more)", naming the first three such paths in byte
// order (v0.1.17 stores.CheckTree, sort.Strings + summarise). A teammate
// still on 0.1.17 reads only that message, so the store's top holds this
// file, whose name sorts before every other path 0.1.17 calls non-lore
// ('.' then '0': folder names start with a letter or digit, _store/ with
// '_', .github/ and .gitignore with ".g") and says what to do. Its name
// is the first thing in 0.1.17's message:
//
//	… is not a lore store (it holds non-lore files such as
//	.0-needs-lore-0.1.18-or-newer, _store/, api/ and 4 more)
//
// lore writes it with the layout commit and with its next commit in a
// store that lacks it (as README.md); every reader of this lore skips it
// (StoreFileAllowed, the hand-edit pick-up's lorePath).

import (
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/errs"
)

// SignpostFile is the signpost's name at the store's top.
const SignpostFile = ".0-needs-lore-0.1.18-or-newer"

// SignpostText is the signpost's one line.
const SignpostText = "this store needs lore 0.1.18 or newer: upgrade lore, then run lore fix\n"

// HasSignpost reports whether the store at dir has the signpost file.
func HasSignpost(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, SignpostFile))
	return err == nil
}

// EnsureSignpost writes the signpost into the store at dir when it has
// none and reports whether it wrote it. A signpost a person changed is
// left as it is.
func EnsureSignpost(dir string) (bool, error) {
	if HasSignpost(dir) {
		return false, nil
	}
	p := filepath.Join(dir, SignpostFile)
	if err := writeAtomic(p, []byte(SignpostText), fileModes{file: readmeMode, dir: sharedDirMode}); err != nil {
		return false, errs.Prefix("rowfs: write "+SignpostFile, err)
	}
	return true, nil
}
