package wire

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/store"
)

// The lazy merged check (MergedCheck) runs before every task list, show,
// next, ...: it reads and decodes every task of every read store to find
// the done tasks without merged_at, then sweeps them against the target
// branch. A repo with hundreds of done tasks that will never be stamped
// (imported, or done before merge tracking) paid that on every command:
// most of a warm task list in a 5,500-row repo (owner's Mac: 4 s, D-110).
//
// mergedMemo remembers that a check over the rows as they are now found
// nothing: Rows names every read store's cache cursor (any row change in
// any store moves one), Tip the target branch commit the sweep used (""
// when there were no candidates, so the tip does not matter). While both
// still hold, the next command skips the check; a write, a refresh that
// brings rows, or a new commit on the target runs it again.
type mergedMemo struct {
	Rows string `json:"rows"`
	Tip  string `json:"tip,omitempty"`
}

// mergedMemoSuffix names the memo file beside the repo store's cache file
// (machine-local, rebuilt like the cache: deleting it only costs one check).
const mergedMemoSuffix = ".merged.json"

// mergedRowsKey is the Rows of a memo for the current state: a hash of
// each read store's cache cursor. ok is false when a cursor cannot be
// read (no memo is used or written then).
func (a *App) mergedRowsKey(ctx context.Context) (key string, ok bool) {
	a.mu.Lock()
	st := a.st
	a.mu.Unlock()
	if st == nil || st.caches == nil {
		return "", false
	}
	h := sha256.New()
	for _, src := range a.ReadStores() {
		cur, err := st.caches.Cursor(ctx, src)
		if err != nil || cur == "" {
			return "", false
		}
		_, _ = h.Write([]byte(string(src) + "\x00" + string(cur) + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// mergedMemoPath is the memo file of this checkout ("" when the repo
// store has no cache file of its own).
func (a *App) mergedMemoPath() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st == nil || a.st.caches == nil {
		return ""
	}
	a.st.caches.mu.Lock()
	defer a.st.caches.mu.Unlock()
	p := a.st.caches.paths[store.SourceRepo]
	if p == "" {
		return ""
	}
	return p + mergedMemoSuffix
}

// readMergedMemo reads the memo; the zero memo when there is none or it
// cannot be read.
func readMergedMemo(path string) mergedMemo {
	var m mergedMemo
	if path == "" {
		return m
	}
	b, err := os.ReadFile(path) //nolint:gosec // lore's own cache folder
	if err != nil || json.Unmarshal(b, &m) != nil {
		return mergedMemo{}
	}
	return m
}

// writeMergedMemo replaces the memo (temp file + rename, so a concurrent
// reader sees the old or the new one), only when it changed. Best effort:
// a memo that cannot be written only means the next command checks again.
func writeMergedMemo(path string, m mergedMemo) {
	if path == "" || m.Rows == "" || readMergedMemo(path) == m {
		return
	}
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}
