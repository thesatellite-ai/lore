package wire

// hookindex.go — what lore's commit hooks may do to the person's index
// (coordinator D3, bug B1-20).
//
//	pre-commit ─▶ forget any old snapshot ─▶ write-tree (the index as the
//	  person staged it) ─▶ stage lore's own row writes, never a pending
//	  `lore fix` format migration (MigrationRewrites) ─▶ write-tree again ─▶
//	  changed: keep "<before> <after> <HEAD>" in the git dir
//	any refusal (pre-commit or the commit-msg leak check), inside a commit
//	  hook ─▶ HEAD is still <HEAD> and the index still <after> (nobody
//	  staged since) ─▶ read-tree <before>: the index is
//	  exactly what the person staged
//
// Before this, the hook added every changed .lore/data file (a whole
// 5,400-file migration) to an unrelated code commit, and a refusal left
// them all staged.

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// hookIndexFile is the snapshot's name in the git dir (git rev-parse
// --git-path: a linked worktree has its own).
const hookIndexFile = "lore-hook-index"

// hookIndexPath is the snapshot file of the repo holding root ("" when not
// a git checkout).
func hookIndexPath(ctx context.Context, root string) string {
	out, err := gitRun{dir: root, args: []string{"rev-parse", "--path-format=absolute", "--git-path", hookIndexFile}}.output(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// indexTree is the tree of the index being committed ("" when it cannot
// be written as one: unmerged entries).
func indexTree(ctx context.Context, root string) string {
	out, err := gitRun{dir: root, env: hookIndexEnv(gitTop(ctx, root)), args: []string{"write-tree"}}.output(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// HookIndexBegin starts a pre-commit: it drops any snapshot an earlier
// commit left and returns the index tree as the person staged it ("" when
// none can be taken: then nothing is restored).
func HookIndexBegin(ctx context.Context, root string) string {
	if p := hookIndexPath(ctx, root); p != "" {
		_ = os.Remove(p) // a stale snapshot must never be restored over a later index
	}
	return indexTree(ctx, root)
}

// headOID is HEAD's commit ("none" before the first commit).
func headOID(ctx context.Context, root string) string {
	out, err := gitRun{dir: root, args: []string{"rev-parse", "-q", "--verify", "HEAD"}}.output(ctx)
	if err != nil {
		return "none"
	}
	return strings.TrimSpace(string(out))
}

// HookIndexStaged records, after the pre-commit staged lore's files, the
// index before and after, so a refusal later in this commit can put the
// person's index back (HookIndexRestore).
func HookIndexStaged(ctx context.Context, root, before string) {
	if before == "" {
		return
	}
	after := indexTree(ctx, root)
	p := hookIndexPath(ctx, root)
	if after == "" || after == before || p == "" {
		return
	}
	_ = os.WriteFile(p, []byte(before+" "+after+" "+headOID(ctx, root)+"\n"), 0o600) // best effort: without it a refusal keeps lore's staging, as before
}

// HookIndexRestore puts back the index the person staged when the commit
// is refused: only when this commit's pre-commit staged something and the
// index is still what it left. It reports whether it restored.
func HookIndexRestore(ctx context.Context, root string) bool {
	p := hookIndexPath(ctx, root)
	if p == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		return false
	}
	_ = os.Remove(p)
	f := strings.Fields(string(b))
	if len(f) != 3 || !inCommitHook() || f[2] != headOID(ctx, root) || indexTree(ctx, root) != f[1] {
		// not inside the commit that staged (a snapshot left by a commit
		// that went through: HEAD moved), or staged since: leave it alone
		return false
	}
	before := f[0]
	_, err = gitRun{dir: root, env: hookIndexEnv(gitTop(ctx, root)), args: []string{"read-tree", before}}.output(ctx)
	return err == nil
}

// MigrationRewrites returns which of paths (relative to the project root,
// under .lore/data) still hold exactly the bytes a finished `lore fix` /
// `lore migrate` wrote (its journal's tree_after): a pending format
// migration the person commits on its own, never staged by the hook into
// another commit. No journal, an unfinished or rolled-back run: none.
func MigrationRewrites(ctx context.Context, root string, paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	j, ok, err := readJournal(filepath.Join(root, filepath.FromSlash(MigrateJournalFile)))
	if err != nil || !ok || j.Phase != phaseDone {
		return nil, nil // no usable record: lore stages as before
	}
	var tree, data string
	for _, p := range j.Places {
		if p.Entry == repoEntry {
			tree, data = p.TreeAfter, path.Clean(filepath.ToSlash(p.Data))
		}
	}
	if tree == "" || data == "" {
		return nil, nil
	}
	ls, err := gitRun{dir: root, args: []string{"-c", "core.quotePath=false", "ls-tree", "-r", "-z", tree}}.output(ctx)
	if err != nil {
		return nil, nil // the tree is gone (gc'd backup): nothing to compare with
	}
	wrote := map[string]string{} // path under root → blob the migration wrote
	for _, ent := range strings.Split(string(ls), "\x00") {
		meta, rel, ok := strings.Cut(ent, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || f[1] != "blob" {
			continue
		}
		wrote[data+"/"+rel] = f[2]
	}
	var cands []string
	for _, p := range paths {
		if _, ok := wrote[p]; !ok || strings.ContainsAny(p, "\n\r") {
			continue
		}
		if _, serr := os.Stat(filepath.Join(root, filepath.FromSlash(p))); serr == nil { // a deletion is never the migration's
			cands = append(cands, p)
		}
	}
	if len(cands) == 0 {
		return nil, nil
	}
	// One git process hashes every candidate as it is on disk now.
	out, err := gitRun{dir: root, args: []string{"hash-object", "--no-filters", "--stdin-paths"}, stdin: []byte(strings.Join(cands, "\n") + "\n")}.output(ctx)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "hook: hash the migrated row files", err)
	}
	ids := strings.Fields(string(out))
	if len(ids) != len(cands) {
		return nil, nil // a file vanished mid-way: stage as before
	}
	res := map[string]bool{}
	for i, p := range cands {
		if ids[i] == wrote[p] {
			res[p] = true
		}
	}
	return res, nil
}
