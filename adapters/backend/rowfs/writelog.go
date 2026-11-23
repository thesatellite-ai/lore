package rowfs

// writelog.go — the files this process wrote, so an IndexView taken
// before lore's own write still holds after it (devco:D-945, bug hunt
// H-203).
//
// Why: a write refreshes the renders of the checkout it ran in (the
// write-render hook, D-921). The render reads the rows again, and every
// remembered IndexView was dropped at the write (writeGen), so each write
// asked git for the data folder's status a second time: one more git
// process per write (~20 ms on macOS), over the budget of D-418. Nothing
// but lore's own files changed between the two questions, and lore knows
// which:
//
//	view (git status, ls-files --stage)  at generation g
//	rowfs writes or removes p1 … pn      generations g+1 … g+n (noteWritten)
//	view after = view with p1 … pn moved from Clean to Check (CarryView)
//
// Check means "lore looks at this file itself", so a written path there is
// read from disk, exactly as git would have listed it after the write
// (changed, untracked, or gone). A path that only might be in the folder
// goes to Check too: a name that is not there is allowed (IndexView.Check).
// When the log no longer holds every write since g (a very large batch),
// CarryView says so and the caller asks git as before.

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// writeLogMax is the most writes the log keeps: a view older than that
// many writes is asked of git again (cheaper than carrying a big import).
const writeLogMax = 4096

// writtenFile is one write or removal noted by noteWritten.
type writtenFile struct {
	gen  uint64 // writeGen after this write
	path string // the absolute path written or removed
}

// writeLog holds the files written since generation floor (exclusive):
// every write with a generation above floor is in recs.
var writeLog struct {
	mu    sync.Mutex
	floor uint64
	recs  []writtenFile
}

// noteWritten bumps writeGen for a write or removal of path and logs it;
// both under the log's lock, so a generation read with the lock held has
// all its writes in the log.
func noteWritten(path string) {
	writeLog.mu.Lock()
	defer writeLog.mu.Unlock()
	g := writeGen.Add(1)
	writeLog.recs = append(writeLog.recs, writtenFile{gen: g, path: path})
	if len(writeLog.recs) > writeLogMax {
		drop := len(writeLog.recs) - writeLogMax/2
		writeLog.floor = writeLog.recs[drop-1].gen
		writeLog.recs = slices.Clone(writeLog.recs[drop:])
	}
}

// writtenSince lists the paths written after generation gen and the
// generation they bring the view to; ok is false when the log no longer
// holds all of them.
func writtenSince(gen uint64) (paths []string, now uint64, ok bool) {
	writeLog.mu.Lock()
	defer writeLog.mu.Unlock()
	now = writeGen.Load()
	if gen < writeLog.floor {
		return nil, now, false
	}
	for _, r := range writeLog.recs {
		if r.gen > gen {
			paths = append(paths, r.path)
		}
	}
	return paths, now, true
}

// CarryView is view, taken of the data folder dataDir at write generation
// gen, as it stands now: each file this process wrote or removed since is
// moved from Clean to Check. ok is false when the writes since gen are no
// longer all known (ask git again). The view passed in is never changed
// (callers share it).
func CarryView(view IndexView, gen uint64, dataDir string) (IndexView, uint64, bool) {
	paths, now, ok := writtenSince(gen)
	if !ok {
		return IndexView{}, now, false
	}
	if len(paths) == 0 {
		return view, now, true
	}
	rels := map[string]bool{}
	for _, p := range paths {
		if rel, in := relUnder(dataDir, p); in {
			rels[rel] = true
		}
	}
	if len(rels) == 0 {
		return view, now, true // writes elsewhere (another folder, a store clone)
	}
	out := IndexView{Clean: make(map[string]string, len(view.Clean)), Check: slices.Clone(view.Check)}
	for rel, blob := range view.Clean {
		if !rels[rel] {
			out.Clean[rel] = blob
		}
	}
	for _, rel := range out.Check {
		delete(rels, rel) // already looked at
	}
	for _, rel := range slices.Sorted(maps.Keys(rels)) {
		out.Check = append(out.Check, rel)
	}
	return out, now, true
}

// relUnder is p relative to dir (slash-separated) when p is inside dir.
// It errs towards "inside": a path spelled through a symlink or in
// another letter case than dir is compared resolved and case-folded too,
// because a path wrongly counted inside only costs one more file lore
// reads itself, while one wrongly counted outside would hide a write.
func relUnder(dir, p string) (string, bool) {
	if rel, ok := relIn(dir, p); ok {
		return rel, true
	}
	rd, derr := filepath.EvalSymlinks(dir)
	rp, perr := filepath.EvalSymlinks(filepath.Dir(p))
	if derr == nil && perr == nil {
		rp = filepath.Join(rp, filepath.Base(p))
		if rel, ok := relIn(rd, rp); ok {
			return rel, true
		}
		if rel, ok := relIn(strings.ToLower(rd), strings.ToLower(rp)); ok {
			return relCase(rp, rel), true
		}
	}
	if rel, ok := relIn(strings.ToLower(dir), strings.ToLower(p)); ok {
		return relCase(p, rel), true
	}
	return "", false
}

// relIn is relUnder's plain textual form.
func relIn(dir, p string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(p))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// relCase is the last len(rel) bytes of p, slash-separated: rel in the
// letter case p was written with (rel came from a case-folded compare).
func relCase(p, rel string) string {
	s := filepath.ToSlash(filepath.Clean(p))
	if len(s) < len(rel) {
		return rel
	}
	return s[len(s)-len(rel):]
}
