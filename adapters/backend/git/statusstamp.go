package git

// statusstamp.go — a `git status` answer kept across commands while the
// files it looked at did not change (bug hunt H-215, devco:D-956). A
// long-lived process (the read server) runs one command after another
// over the same checkouts; every warm `lore ask` ran `git status` once in
// each store clone (the pick-up of hand edits, HandChanges) and once over
// the code repo's data folder (repoStatus): four git processes, ~7 ms each
// on a loaded box, a third of the call, to learn again that nothing
// changed. git answers status by comparing every file's stat with the
// index; lore makes the same comparison in-process (one lstat per file,
// no process) against the stat it took when git last ran, and reuses
// git's answer while every stat, the index file, HEAD and the ignore files
// are the same.
//
// Racy files: a file written in the same instant as the stat was taken
// could change again without changing its stat. A stamp holding any file
// changed in the last statusSettle is never reused (git runs every time
// while files are fresh), the same guard git keeps for racy index entries.

import (
	"context"
	"encoding/binary"
	"errors"
	"hash"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// statusSettle is how old every file under a stamp must be before the
// stamp may stand in for git (see the file comment).
const statusSettle = 2 * time.Second

// treeStamp sums the stat of every file and folder a status reads.
type treeStamp struct {
	// tree sums the work tree's files, the ignore files and HEAD.
	tree uint64
	// index sums the index file, which git may rewrite while it answers.
	index uint64
	// settled is false when any file changed within statusSettle of the
	// stamp, a walk failed, or the index is unknown: such a stamp never
	// matches.
	settled bool
}

// stampInput is what one stamp covers.
type stampInput struct {
	// roots are walked whole (a ".git" folder in them is skipped).
	roots []string
	// files are stat-ed one by one (ignore files); a missing file counts
	// as missing, which is part of the sum too.
	files []string
	// index is the repository's index file ("" unknown: nothing is kept).
	index string
	// head is the commit HEAD names (status compares the index with it).
	head string
	// gen is rowfs.WriteGeneration: a row file this process wrote.
	gen uint64
}

// stampOf takes the stamp of in at now.
func stampOf(in stampInput, now time.Time) treeStamp {
	settled := in.index != ""
	var buf [8]byte
	num := func(h hash.Hash64, v int64) {
		binary.LittleEndian.PutUint64(buf[:], uint64(v))
		_, _ = h.Write(buf[:])
	}
	add := func(h hash.Hash64, p string, info fs.FileInfo) {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
		if info == nil {
			num(h, -1)
			return
		}
		num(h, int64(info.Mode()))
		num(h, info.Size())
		num(h, info.ModTime().UnixNano())
		if now.Sub(info.ModTime()) < statusSettle {
			settled = false
		}
	}
	lstat := func(p string) fs.FileInfo {
		info, err := os.Lstat(p)
		if err != nil {
			return nil
		}
		return info
	}
	tree, index := fnv.New64a(), fnv.New64a()
	if in.index != "" {
		add(index, in.index, lstat(in.index))
	}
	_, _ = tree.Write([]byte(in.head))
	num(tree, int64(in.gen))
	for _, f := range in.files {
		add(tree, f, lstat(f))
	}
	for _, root := range in.roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == root && errors.Is(err, fs.ErrNotExist) {
					add(tree, p, nil) // no folder yet: that too is part of the answer
					return nil
				}
				return err
			}
			if d.IsDir() && d.Name() == ".git" {
				return filepath.SkipDir
			}
			info, ierr := d.Info()
			if ierr != nil {
				return ierr
			}
			add(tree, p, info)
			return nil
		})
		if err != nil {
			settled = false
		}
	}
	return treeStamp{tree: tree.Sum64(), index: index.Sum64(), settled: settled}
}

// keptStatus is one status answer kept across commands.
type keptStatus struct {
	stamp treeStamp
	out   []byte
}

// keptStatuses are the kept answers by checkout and question.
var keptStatuses = struct {
	mu sync.Mutex
	by map[string]keptStatus
}{by: map[string]keptStatus{}}

// statusKept runs run (one `git status`) unless the answer kept under key
// was taken from the same stamp; before is the stamp taken before run, so
// a file changed while git ran makes the next call run git again. A
// failed run keeps nothing.
func statusKept(key string, in func() stampInput, run func() ([]byte, error)) ([]byte, error) {
	before := stampOf(in(), time.Now())
	keptStatuses.mu.Lock()
	k, ok := keptStatuses.by[key]
	keptStatuses.mu.Unlock()
	if ok && before.settled && k.stamp == before {
		return k.out, nil
	}
	out, err := run()
	keptStatuses.mu.Lock()
	defer keptStatuses.mu.Unlock()
	delete(keptStatuses.by, key)
	if err != nil || !before.settled {
		return out, err
	}
	// git may refresh the index's stat data while it reads: the index is
	// stamped as status left it, the work tree as it was before git ran, so
	// a file changed while git ran misses next time (devco:D-418: keyed on
	// the index from before, every next call missed)
	if after := stampOf(in(), time.Now()); after.settled {
		keptStatuses.by[key] = keptStatus{stamp: treeStamp{tree: before.tree, index: after.index, settled: true}, out: out}
	}
	return out, nil
}

// statusFiles are the single files a status of b depends on beside the
// walked folders: the index first, then the repository's info/exclude and
// the user's global ignore file (found once per Backend; none when git
// cannot say, and then nothing is kept).
func (b *Backend) statusFiles(ctx context.Context) []string {
	b.statusFilesOnce.Do(func() {
		out, err := b.git.run(ctx, call{dir: b.work, args: []string{"rev-parse", "--path-format=absolute", "--git-path", "index", "--git-path", "info/exclude"}})
		if err != nil {
			return
		}
		files := strings.Split(trimmed(out), "\n")
		if len(files) != 2 {
			return
		}
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			files = append(files, filepath.Join(xdg, "git", "ignore"))
		} else if home, herr := os.UserHomeDir(); herr == nil {
			files = append(files, filepath.Join(home, ".config", "git", "ignore"))
		}
		b.statusFilesList = files
	})
	return b.statusFilesList
}

// cloneStatusStamp is the stamp input of a store clone's whole status
// (HandChanges): every file of the clone.
func (b *Backend) cloneStatusStamp(ctx context.Context) stampInput {
	in := b.statusStampInput(ctx)
	in.roots = []string{b.work}
	return in
}

// statusStampInput is the part of every status stamp: the index, the
// ignore files beside the work tree, HEAD and this process's writes.
func (b *Backend) statusStampInput(ctx context.Context) stampInput {
	in := stampInput{gen: rowfs.WriteGeneration()}
	if files := b.statusFiles(ctx); len(files) > 0 {
		in.index, in.files = files[0], slices.Clone(files[1:])
	}
	if head, err := b.head(ctx); err == nil {
		in.head = head
	} else {
		in.index = "" // HEAD unknown: nothing is kept
	}
	return in
}

// repoStatusStamp is the stamp input of the code repo's data folder status
// (repoStatus): the data folder, and the .gitignore of every folder from
// it up to the file system's root (git reads those up to the repo's top;
// one more above it only costs a stat).
func (b *Backend) repoStatusStamp(ctx context.Context) stampInput {
	in := b.statusStampInput(ctx)
	data := filepath.Join(b.work, filepath.FromSlash(b.folder.DataPath()))
	for d := data; ; d = filepath.Dir(d) {
		in.files = append(in.files, filepath.Join(d, ".gitignore"))
		if d == filepath.Dir(d) {
			break
		}
	}
	in.roots = []string{data}
	return in
}
