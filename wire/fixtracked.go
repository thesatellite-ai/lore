package wire

// fixtracked.go — the repo side of the fix run's tracked files step
// (opsdoctor.TrackedFilesEnv, devco:D-744): which tracked files differ
// from HEAD, each with a fingerprint of its state, read before and after
// the run so every tracked file the run changed is named for its commit.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// trackedDeleted is the fingerprint of a tracked file missing from the
// work tree.
const trackedDeleted = "deleted"

// TrackedChanges implements opsdoctor.TrackedFilesEnv: for every tracked
// path that differs from HEAD (index or work tree), its git status code
// and a hash of its bytes now. ok is false outside a git repo or when git
// fails. Untracked files are not read (lore's new files have their own
// lines: the git wiring and agent hooks steps).
func (a *App) TrackedChanges(ctx context.Context) (map[string]string, bool) {
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" {
		return nil, false
	}
	// --no-optional-locks: a read must not take the index lock a
	// concurrent git command in the repo holds; --no-renames keeps one
	// path per entry
	out, err := gitRun{dir: top, args: []string{"--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=no", "--no-renames"}}.output(ctx)
	if err != nil {
		return nil, false
	}
	return trackedFingerprints(top, out), true
}

// trackedFingerprints parses `git status --porcelain=v1 -z` (entries
// "XY path", NUL separated) into path → "XY:<sha256 of the file>".
func trackedFingerprints(top string, porcelain []byte) map[string]string {
	got := map[string]string{}
	for _, e := range bytes.Split(porcelain, []byte{0}) {
		if len(e) < 4 {
			continue
		}
		code, path := string(e[:2]), string(e[3:])
		fp := trackedDeleted
		if b, err := os.ReadFile(filepath.Join(top, filepath.FromSlash(path))); err == nil {
			sum := sha256.Sum256(b)
			fp = hex.EncodeToString(sum[:])
		}
		got[path] = code + ":" + fp
	}
	return got
}
