package wire

// doctor_unmerged.go — E-8: a git merge, rebase, cherry-pick or revert
// that stopped on lore's files (often across `lore migrate run`, in a
// clone where lore only read so far and so had no merge driver) left
// unmerged paths under .lore/data. Conflict markers were counted, but a
// modify/delete conflict has none: doctor said "open conflicts: 0" and
// named no next step. The [sync] section now counts every unmerged lore
// file and names the operation and the way out.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// gitOpInProgress names the git operation a stopped work tree is in ("" when
// none): the marker file or folder git keeps in the git dir.
func gitOpInProgress(ctx context.Context, dir string) string {
	for _, op := range []struct{ path, name string }{
		{"rebase-merge", "rebase"}, {"rebase-apply", "rebase"}, {"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"},
	} {
		out, err := gitRun{dir: dir, args: []string{"rev-parse", "--git-path", op.path}}.output(ctx)
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(out))
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if _, serr := os.Stat(p); serr == nil {
			return op.name
		}
	}
	return ""
}

// unmergedLoreFiles lists the unmerged paths under dataRel, each once, in
// git's order (modify/delete conflicts included).
func unmergedLoreFiles(ctx context.Context, dir, dataRel string) []string {
	out, err := gitRun{dir: dir, args: []string{"ls-files", "-u", "-z", "--", dataRel}}.output(ctx)
	if err != nil {
		return nil
	}
	var paths []string
	for _, e := range nulFields(out) {
		_, p, ok := strings.Cut(e, "\t")
		if ok && (len(paths) == 0 || paths[len(paths)-1] != p) {
			paths = append(paths, p)
		}
	}
	return paths
}

// unmergedLine is the doctor line for unmerged lore files ("" when there
// are none): the operation, how many files and the first, and the next
// step. Without lore's merge driver in the clone the way out is to abort,
// let lore sync set the driver up, and run the operation again, which
// then merges lore's files by itself; with it, the person resolves the
// files git could not and continues.
func unmergedLine(op string, files []string, driver bool) string {
	if len(files) == 0 {
		return ""
	}
	if op == "" {
		op = "merge"
	}
	head := "a git " + op + " stopped with " + strconv.Itoa(len(files)) + " lore file(s) unmerged under .lore/data (" + files[0]
	if len(files) > 1 {
		head += " and " + strconv.Itoa(len(files)-1) + " more"
	}
	head += "): "
	if !driver {
		return head + "this clone has no lore merge driver yet, so git merged lore's files as text; run git " + op + " --abort, then lore sync (sets up the merge driver), then the " + op + " again"
	}
	return head + "keep the right side of each (git add <file>, or git rm <file> for a row deleted on one side), then git " + op + " --continue"
}
