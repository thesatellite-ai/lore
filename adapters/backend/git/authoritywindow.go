package git

// authoritywindow.go — the roster replay reads row versions a window of
// files at a time (bug hunt H-280, devco:D-1018). replayChunk decided a
// chunk of commits with every row version its commits named read and
// decoded up front. A chunk of ordinary commits names a few thousand, but
// a compacted store's history starts with its snapshot: one commit that
// holds every row file. At 1.26M row files that one read held every
// version decoded at once (~1.7 GB) and the collector spent most of the
// replay scanning it. Now only the roster's own rows, which a commit's
// checks read across its files (crossFile), are read for the whole
// chunk; every other file's versions are read when the replay reaches
// it, replayWindowFiles files at a time. Each file is decided with the
// same versions as before, so the decisions are the same.

import (
	"bytes"
	"context"
	"encoding/json"
	"runtime"
	"sync"
)

// replayWindowFiles is how many row files the replay reads the versions
// of at once (besides the roster's own rows): one cat-file process per
// window, a few MB decoded. A var: a test decides with a small window.
var replayWindowFiles = 16384

// crossFile reports whether table's rows are read by a commit's checks
// beyond the file itself (refusal, addsPerson, authorActor, ownsIn):
// such versions are read for the whole chunk up front.
func crossFile(table string) bool {
	return rosterTables[table] || membershipTables[table] || teamInfoTables[table]
}

// replayWindow holds the row versions replayChunk reads for its commits:
// fixed (the crossFile rows of the whole chunk) and docs, the versions
// the files from the window's start up to (endC, endF) need.
type replayWindow struct {
	b        *Backend
	commits  []replayCommit
	accepted map[string]string // the replay's accepted versions (authority.accepted), read when a window starts
	fixed    map[string]rosterDoc
	docs     map[string]rosterDoc
	// endC, endF: the first change (commit, change index) past the window
	endC, endF int
}

// docOf is the decoded row version sha, if the replay read it.
func (w *replayWindow) docOf(sha string) (rosterDoc, bool) {
	if d, ok := w.fixed[sha]; ok {
		return d, true
	}
	d, ok := w.docs[sha]
	return d, ok
}

// reach makes sure the versions change fi of commit ci needs are read:
// when it is past the window, the next window starts there. A window
// reads each file's new version and, for a pinnable table, the version it
// replaced and the one the replay accepted last (roleRefusal). The
// accepted version is read when the window starts: a file of the window
// that changes it later names the new one itself, so it is read as well.
func (w *replayWindow) reach(ctx context.Context, ci, fi int) error {
	if ci < w.endC || (ci == w.endC && fi < w.endF) {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	want := func(sha string) {
		if sha == "" || zeroBlob(sha) || seen[sha] {
			return
		}
		if _, ok := w.fixed[sha]; ok {
			return
		}
		seen[sha] = true
		names = append(names, sha)
	}
	files := 0
	c, f := ci, fi
scan:
	for ; c < len(w.commits); c, f = c+1, 0 {
		changes := w.commits[c].changes
		for ; f < len(changes); f++ {
			ch := changes[f]
			if !isRowFile(ch.path) || crossFile(tableOfPath(ch.path)) {
				continue
			}
			if files == replayWindowFiles {
				break scan
			}
			files++
			want(ch.newBlob)
			if pinnable(tableOfPath(ch.path)) {
				// the role rule judges the version a commit replaced
				// (devco D-851): an incremental replay has not read it
				want(ch.oldBlob)
				want(w.accepted[ch.path])
			}
		}
	}
	if c == ci && f == fi {
		// nothing but crossFile rows from here: they are all in fixed
		c, f = len(w.commits), 0
	}
	w.endC, w.endF = c, f
	w.docs = nil // the last window's versions go before the next is read
	docs, err := w.b.replayDocs(ctx, names)
	if err != nil {
		return err
	}
	w.docs = docs
	notePeak(len(w.fixed) + len(names))
	return nil
}

// replayDocs reads and decodes the row versions names (git blob ids),
// with each one's content key; a version that is not a row is left out.
// Only the fields the checks read stay in memory, never the file itself.
// The decoding runs on up to replayParseWorkers goroutines: each version
// is decoded on its own, and a window of them is most of the replay's
// CPU (a compacted store's snapshot is every row file of the store).
func (b *Backend) replayDocs(ctx context.Context, names []string) (map[string]rosterDoc, error) {
	raws, err := b.replayBlobs(ctx, names)
	if err != nil {
		return nil, err // typed by readBlobsBy
	}
	docs := make([]rosterDoc, len(names))
	rows := make([]bool, len(names))
	decode := func(lo, hi int) {
		for i := lo; i < hi; i++ {
			raw, ok := raws[names[i]]
			if !ok {
				continue
			}
			var d rosterDoc
			if json.Unmarshal(bytes.TrimSpace(raw), &d) == nil {
				d.content = contentKey(raw)
				docs[i], rows[i] = d, true
			}
		}
	}
	workers := min(runtime.GOMAXPROCS(0), replayParseWorkers, max(1, len(names)/replayParseMin))
	step := (len(names) + workers - 1) / max(1, workers)
	var wg sync.WaitGroup
	for lo := 0; lo < len(names); lo += step {
		hi := min(lo+step, len(names))
		wg.Go(func() { decode(lo, hi) })
	}
	wg.Wait()
	parsed := make(map[string]rosterDoc, len(names))
	for i, ok := range rows {
		if ok {
			parsed[names[i]] = docs[i]
		}
	}
	return parsed, nil
}

// replayParseWorkers is the most goroutines replayDocs decodes on: a
// share of a shared machine, not all of it.
const replayParseWorkers = 4

// replayParseMin is the fewest versions worth a goroutine of their own. A
// var: a test decodes a few versions on several goroutines.
var replayParseMin = 256
