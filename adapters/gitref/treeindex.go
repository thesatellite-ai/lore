package gitref

// treeindex.go — ReadFile without one git call per file (owner bug 15:
// `lore fix --dry-run` in a repo of ~2,000 rows ran `git ls-tree -r
// --full-tree HEAD -- .lore/data/<table>/<id>.json` and a cat-file for
// every row, on HEAD and again on origin/<branch>: thousands of git
// processes, 49 s).
//
//	ReadFile(rev, "a/b/c.json")
//	  ─▶ ResolveCommit(rev)                       (one rev-parse; ReadCache keeps it)
//	  ─▶ listing of (commit, "a/b")               one `ls-tree -r -z` per directory
//	  │                                            per commit, kept for the Repo's life
//	  ─▶ not listed: (nil, false)                  no further call
//	  ─▶ listed: the blob's bytes                  one `cat-file --batch` for every
//	                                               blob of that directory, kept by id
//
// A commit's tree and a blob's bytes never change for the same object id,
// so the memo can never go stale; a ref that moves resolves to another
// commit and gets its own listing.

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// treeEntry is one file of a directory listing.
type treeEntry struct {
	mode string
	oid  string
}

// treeIndex is the Repo's memo (see the file comment).
type treeIndex struct {
	mu sync.Mutex
	// dirs maps commit + "\x00" + dir to that directory's files (every
	// depth, root-relative paths).
	dirs map[string]map[string]treeEntry
	// blobs maps a blob id to its bytes.
	blobs map[string][]byte
}

// newTreeIndex returns an empty memo.
func newTreeIndex() *treeIndex {
	return &treeIndex{dirs: map[string]map[string]treeEntry{}, blobs: map[string][]byte{}}
}

// cutLastSlash splits p at its last "/" (nested false when it has none).
func cutLastSlash(p string) (dir, base string, nested bool) {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "", p, false
	}
	return p[:i], p[i+1:], true
}

// readIndexed is ReadFile through the memo: path's directory dir is listed
// once per commit and its blobs read in one batch.
func (r *Repo) readIndexed(ctx context.Context, rev, path, dir string) ([]byte, bool, error) {
	clean, err := cleanPath(path)
	if err != nil {
		return nil, false, err
	}
	cleanDir, _, _ := cutLastSlash(clean)
	commit, ok, err := r.ResolveCommit(ctx, rev)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, fmt.Errorf("%w: %s", ErrNotFound, rev)
	}
	listing, err := r.dirListing(ctx, commit, cleanDir)
	if err != nil {
		return nil, false, err
	}
	e, found := listing[clean]
	if !found {
		return nil, false, nil
	}
	if e.mode != modeRegular && e.mode != modeExecutable {
		return nil, false, fmt.Errorf("%w: %s (mode %s)", ErrUnsupportedEntry, clean, e.mode)
	}
	b, err := r.blobOf(ctx, e.oid, listing)
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// dirListing is every file under dir in commit (one ls-tree, memoized).
func (r *Repo) dirListing(ctx context.Context, commit, dir string) (map[string]treeEntry, error) {
	key := commit + "\x00" + dir
	r.idx.mu.Lock()
	l, ok := r.idx.dirs[key]
	r.idx.mu.Unlock()
	if ok {
		return l, nil
	}
	out, err := r.git.Run(ctx, Invocation{Dir: r.dir, Args: []string{"ls-tree", "-r", "-z", "--full-tree", commit, "--", dir}})
	if err != nil {
		return nil, err
	}
	l = map[string]treeEntry{}
	for rec := range strings.SplitSeq(string(out), "\x00") {
		if rec == "" {
			continue
		}
		meta, path, ok := strings.Cut(rec, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != lsTreeFields {
			return nil, fmt.Errorf("gitref: unexpected ls-tree record %q", rec)
		}
		l[path] = treeEntry{mode: fields[0], oid: fields[2]}
	}
	r.idx.mu.Lock()
	r.idx.dirs[key] = l
	r.idx.mu.Unlock()
	return l, nil
}

// blobOf is blob oid's bytes; on a miss every regular blob of listing not
// read yet is read in the same cat-file --batch (the next rows asked for
// are almost always its neighbours).
func (r *Repo) blobOf(ctx context.Context, oid string, listing map[string]treeEntry) ([]byte, error) {
	r.idx.mu.Lock()
	b, ok := r.idx.blobs[oid]
	var want []string
	if !ok {
		seen := map[string]bool{oid: true}
		want = append(want, oid)
		for _, e := range listing {
			if (e.mode == modeRegular || e.mode == modeExecutable) && !seen[e.oid] {
				if _, have := r.idx.blobs[e.oid]; !have {
					seen[e.oid] = true
					want = append(want, e.oid)
				}
			}
		}
	}
	r.idx.mu.Unlock()
	if ok {
		return b, nil
	}
	blobs, err := r.catBlobs(ctx, want)
	if err != nil {
		return nil, err
	}
	r.idx.mu.Lock()
	defer r.idx.mu.Unlock()
	for i, id := range want {
		r.idx.blobs[id] = blobs[i]
	}
	return r.idx.blobs[oid], nil
}
