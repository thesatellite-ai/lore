package host

// mergedriver_base.go — the common copy of a row git calls add/add
// (devco Q-6). git hands the merge driver an empty %O when the merge base
// holds no copy of the path: a row cherry-picked onto a release branch
// lives on both branches but on neither's common ancestor. Merged with no
// base, every field the two sides hold differently counts as changed on
// both, so the newer side's stale copy of a field the other side edited
// wins and that edit is lost. Both histories still hold the copy the row
// started from (the cherry-pick wrote the same blob), so the newest blob
// of the path found in both histories is a true common base: each side's
// edit to a different field is kept, and only a clash on one field falls
// to the add/add rule (newer edit wins, replaced value named).

import (
	"context"
	"os"
	"strings"
)

// gitHeadEnv is the prefix of the variables git sets for a merge driver
// during `git merge`: GITHEAD_<sha of the other side>=<its name>. git
// writes MERGE_HEAD only after the drivers ran, so this is the one place
// the other side is named while the driver runs.
const gitHeadEnv = "GITHEAD_"

// historyBase returns the newest version of repoPath that both HEAD's and
// the other side's history hold, or nil when there is none (no repo, no
// other side, a path added independently on each side). The other side is
// the GITHEAD_<sha> commit of a merge; a cherry-pick or rebase names none,
// so it is the newest commit (any branch, or ORIG_HEAD: the branch being
// rebased) whose copy of repoPath is exactly theirs. Any git failure
// yields nil: the driver then merges as before (add/add rule).
func historyBase(ctx context.Context, repoPath, theirsPath string) []byte {
	if repoPath == "" {
		return nil
	}
	ours := pathBlobs(ctx, "HEAD", repoPath)
	if len(ours) == 0 {
		return nil
	}
	inTheirs := map[string]bool{}
	for _, rev := range theirsRevs(ctx, repoPath, theirsPath) {
		for _, b := range pathBlobs(ctx, rev, repoPath) {
			inTheirs[b] = true
		}
	}
	for _, b := range ours { // newest first
		if !inTheirs[b] {
			continue
		}
		out, err := gitOutput(ctx, "cat-file", "blob", b)
		if err != nil {
			return nil
		}
		return out
	}
	return nil
}

// theirsRevs names the commit(s) of the other side (see historyBase).
func theirsRevs(ctx context.Context, repoPath, theirsPath string) []string {
	var revs []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if sha, ok := strings.CutPrefix(name, gitHeadEnv); ok && sha != "" {
			revs = append(revs, sha)
		}
	}
	if len(revs) > 0 {
		return revs
	}
	out, err := gitOutput(ctx, "hash-object", "--no-filters", "--", theirsPath)
	if err != nil {
		return nil
	}
	want := strings.TrimSpace(string(out))
	args := []string{"log", "--no-abbrev", "--format=%H", "--raw", "--no-renames", "--all"}
	if _, err := gitOutput(ctx, "rev-parse", "--verify", "--quiet", "ORIG_HEAD^{commit}"); err == nil {
		args = append(args, "ORIG_HEAD")
	}
	if out, err = gitOutput(ctx, append(args, "--", repoPath)...); err != nil {
		return nil
	}
	commit := ""
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case line == "":
		case !strings.HasPrefix(line, ":"):
			commit = line
		default:
			if f := strings.Fields(line); len(f) >= 5 && f[3] == want && commit != "" {
				return []string{commit}
			}
		}
	}
	return nil
}

// nullBlob is the all-zero id git prints for "no blob" (a delete).
const nullBlob = "0000000000000000000000000000000000000000"

// pathBlobs lists the blob ids repoPath had in rev's history, newest
// first. The raw diff lines are ":<mode> <mode> <old> <new> <status>\t<path>";
// only the path asked for is listed, so the path itself is never parsed
// (a quoted non-ASCII name cannot confuse it).
func pathBlobs(ctx context.Context, rev, repoPath string) []string {
	out, err := gitOutput(ctx, "log", "--no-abbrev", "--format=", "--raw", "--no-renames", rev, "--", repoPath)
	if err != nil {
		return nil
	}
	var blobs []string
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, ":") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 5 || f[3] == nullBlob || strings.Trim(f[3], "0") == "" {
			continue
		}
		blobs = append(blobs, f[3])
	}
	return blobs
}
