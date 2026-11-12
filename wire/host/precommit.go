package host

// precommit.go — `lore sync hook pre-commit` on a binary built from the root
// module (v0.1.17 sync_cmd.go preCommitCheck, minus its lore.db sync pass):
//
//	git commit ─▶ pre-commit block ─▶ project root (none: not a lore
//	project, git is never blocked) ─▶ changed files under .lore/data ─▶
//	any of them broken (invalid JSON, conflict markers at any depth)?
//	refuse, naming each (OS clutter such as .DS_Store is skipped, never
//	checked or staged) ─▶ else stage .lore/data (knowledge is never left
//	behind by a partial `git add`) ─▶ RX7: a staged meta_keys row that
//	drops internal, sensitive or strict without a new changed_by record
//	refuses with E_LEAK_BLOCKED (wire.KeyLoosenings)
//
// Secrets and F16 in the staged files are the commit-msg block's job
// (`lore check leaks --staged`), which runs after this one has staged.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/dataignore"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/logic/merge3"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/wire"
)

// Reasons a changed lore file is refused (printed after its path).
const (
	brokenConflictMarkers = "conflict markers"
	brokenInvalidJSON     = "invalid JSON"
	brokenMetaConflict    = "unsettled metadata conflict in "
)

// migrationLeftOut is the line of a commit made while a `lore fix` format
// migration is still uncommitted: its files are not staged with it.
const migrationLeftOut = "lore: %d row files rewritten by lore fix (format migration) are not added to this commit; commit them on their own: git add .lore/data && git commit -m \"lore: migrate format\""

// brokenFilesHint is the next step of a refused pre-commit.
const brokenFilesHint = "fix the file by hand (a row file is one JSON object; settle a conflict with the row's edit command), or restore it with `git checkout -- <path>`; then commit again"

// loreDataPathspec is the project's row folder, relative to its root.
var loreDataPathspec = filepath.ToSlash(filepath.Join(rowfs.LoreDir, rowfs.DataDir))

// preCommit is `lore sync hook pre-commit` (see the file comment).
func (h *liveHost) preCommit(ctx context.Context) error {
	if top, isGit, inStore := rowfs.StoreTop(h.cfg.WorkDir); inStore && isGit {
		return h.storePreCommit(ctx, top) // a hand commit in a store clone (precommit_store.go)
	}
	root := liveProjectRoot(h.cfg.WorkDir)
	if root == "" {
		return nil // not a lore project: never block git over it
	}
	repo, err := gitref.Open(ctx, root, h.cfg.Git)
	if err != nil {
		return nil // not a git checkout (a hook copied around)
	}
	changed, junk, err := changedUnder(ctx, repo, root, loreDataPathspec)
	if err != nil {
		return errs.Wrap(errs.Internal, "pre-commit: list the changed lore files", err)
	}
	// A branch file of a row on the live ref is shadowed by live (fixed
	// rule 4): neither checked nor staged, so a stale or hand-edited copy
	// never rides into the user's commit (F6 "dirty landed path").
	changed, shadowed, err := withoutLiveRows(ctx, repo, root, changed)
	if err != nil {
		return errs.Wrap(errs.Internal, "pre-commit: read the local live ref", err)
	}
	// D3 (B1-20): a pending `lore fix` format migration is committed on its
	// own, never swept into this commit; the rest of lore's writes are
	// staged as before. What the person staged themselves stays staged.
	pending, err := wire.MigrationRewrites(ctx, root, changed)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		kept := changed[:0:0]
		for _, p := range changed {
			if !pending[p] {
				kept = append(kept, p)
			}
		}
		changed = kept
		if n := unstagedCount(ctx, repo, root, pending); n > 0 {
			h.line(fmt.Sprintf(migrationLeftOut, n))
		}
	}
	var bad []string
	for _, p := range changed {
		b, rerr := os.ReadFile(filepath.Clean(filepath.Join(root, filepath.FromSlash(p)))) // p is a path git listed under .lore/data
		if errors.Is(rerr, os.ErrNotExist) {
			continue // a deletion: staging records it
		}
		if rerr != nil {
			return errs.Wrap(errs.Internal, "pre-commit: read "+p, rerr)
		}
		if why := brokenLoreFile(b); why != "" {
			bad = append(bad, p+" ("+why+")")
		}
	}
	if len(bad) > 0 {
		return errs.New(errs.InvalidInput, "refusing to commit broken lore files: "+strings.Join(bad, ", ")).WithHint(brokenFilesHint)
	}
	if len(changed) > 0 {
		before := wire.HookIndexBegin(ctx, root) // the index as the person staged it
		inv := gitref.Invocation{Dir: root, Args: []string{"add", "-A", "--", loreDataPathspec}, Env: hookIndexEnv(ctx, repo, root)}
		if shadowed || junk || len(pending) > 0 {
			// stage the rest one by one, leaving the shadowed files, OS
			// clutter and the pending migration out; the list goes on
			// stdin (thousands of paths overflow a command line)
			inv.Args = []string{"add", "-A", "--pathspec-from-file=-", "--pathspec-file-nul"}
			inv.Stdin = []byte(strings.Join(changed, "\x00"))
		}
		if _, err := repo.Run(ctx, inv); err != nil {
			wire.HookIndexRestore(ctx, root)
			return errs.Wrap(errs.Internal, "pre-commit: stage "+loreDataPathspec, err)
		}
		wire.HookIndexStaged(ctx, root, before)
	} else {
		wire.HookIndexBegin(ctx, root) // nothing staged: no old snapshot may be restored later
	}
	// RX7 (OV64). No HEAD yet (a first commit) is a git error here, and
	// then there is nothing to compare.
	found, lerr := wire.KeyLoosenings(ctx, root, "HEAD", "")
	if lerr != nil {
		return nil
	}
	if rerr := looseningError(found); rerr != nil {
		wire.HookIndexRestore(ctx, root) // refused: the index is what the person staged
		return rerr
	}
	return nil
}

// changedUnder lists the files under pathspec that differ from HEAD or are
// untracked, as paths relative to root (git status, porcelain v1 with -z:
// "XY <path>", a rename or copy followed by its old path).
// junk reports that OS clutter (adapters/dataignore.IsOSJunk) was listed
// and left out: staging then names the files instead of the folder.
func changedUnder(ctx context.Context, repo *gitref.Repo, root, pathspec string) (paths []string, junk bool, err error) {
	out, err := repo.Run(ctx, gitref.Invocation{Dir: root, Args: []string{"-c", "core.quotePath=false", "status", "--porcelain", "-z", "--untracked-files=all", "--", pathspec}})
	if err != nil {
		return nil, false, err
	}
	top, err := repo.Run(ctx, gitref.Invocation{Dir: root, Args: []string{"rev-parse", "--show-prefix"}})
	if err != nil {
		return nil, false, err
	}
	prefix := strings.TrimSpace(string(top)) // root relative to the top, "" or "sub/"
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < len("XY ")+1 {
			continue
		}
		status, p := f[:2], f[3:]
		if strings.ContainsAny(status, "RC") {
			i++ // the old path of a rename or copy
		}
		if dataignore.IsOSJunk(p) {
			junk = true // Finder's .DS_Store, ._name: not lore's, never checked or staged
			continue
		}
		paths = append(paths, strings.TrimPrefix(p, prefix))
	}
	return paths, junk, nil
}

// unstagedCount is how many of paths (relative to root) differ between the
// work tree and the index being committed: the migrated files the person
// has not staged themselves (those already staged are committed as staged).
func unstagedCount(ctx context.Context, repo *gitref.Repo, root string, paths map[string]bool) int {
	out, err := repo.Run(ctx, gitref.Invocation{Dir: root, Env: hookIndexEnv(ctx, repo, root),
		Args: []string{"-c", "core.quotePath=false", "diff", "--name-only", "--relative", "-z", "--", loreDataPathspec}})
	if err != nil {
		return len(paths)
	}
	n := 0
	for _, p := range strings.Split(string(out), "\x00") {
		if paths[p] {
			n++
		}
	}
	return n
}

// withoutLiveRows drops from changed (paths relative to root) the row
// files whose row is on the local live ref; shadowed reports whether any
// was dropped. Offline: only the local ref is read, no session is opened.
func withoutLiveRows(ctx context.Context, repo *gitref.Repo, root string, changed []string) (kept []string, shadowed bool, err error) {
	if len(changed) == 0 {
		return changed, false, nil
	}
	ref := &live.Ref{Repo: repo, Name: live.RefLive}
	oid, ok, err := repo.ResolveRef(ctx, ref.Name)
	if err != nil || !ok {
		return changed, false, err
	}
	tree, err := ref.ReadTree(ctx, oid)
	if err != nil {
		return nil, false, err
	}
	_, folder := livePlace(root)
	rows, _ := tree.Rows(folder) // an unreadable live row shadows nothing here; lore doctor reports it
	prefix := loreDataPathspec + "/"
	for _, p := range changed {
		rel, isRow := strings.CutPrefix(p, prefix)
		table, file, nested := strings.Cut(rel, "/")
		id, isJSON := strings.CutSuffix(file, rowFileExt)
		if isRow && nested && isJSON && !strings.Contains(file, "/") {
			if _, onLive := rows[live.RowKey{Table: table, ID: id}]; onLive {
				shadowed = true
				continue
			}
		}
		kept = append(kept, p)
	}
	return kept, shadowed, nil
}

// brokenLoreFile is why b cannot be committed as a lore file ("" = fine):
// conflict markers in the text or in any string value (meta and fields
// entries nest, METADATA_DESIGN 3.x), not one JSON document, or a row
// whose meta holds a structured conflict entry (METADATA 3.12: refused by
// the pre-commit check until a person settles it; merge.MetaConflicts is
// the one scanner). A file that is not a row (a folder's _meta.json) has
// no meta to scan.
func brokenLoreFile(b []byte) string {
	if why := brokenLoreText(b); why != "" {
		return why
	}
	if r, err := row.Decode(b); err == nil {
		if ks := merge.MetaConflicts(r); len(ks) > 0 {
			return brokenMetaConflict + strings.Join(ks, ", ")
		}
	}
	return ""
}

// brokenLoreText is brokenLoreFile's byte-level half: markers or not JSON.
func brokenLoreText(b []byte) string {
	if merge3.ContainsConflictMarkers(string(b)) {
		return brokenConflictMarkers
	}
	if !json.Valid(b) {
		return brokenInvalidJSON
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	for {
		t, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return ""
		}
		if err != nil {
			return brokenInvalidJSON
		}
		if s, ok := t.(string); ok && merge3.ContainsConflictMarkers(s) {
			return brokenConflictMarkers + " in a value"
		}
	}
}

// looseningError joins the RX7 findings into one E_LEAK_BLOCKED (nil: none).
func looseningError(found []error) error {
	if len(found) == 0 {
		return nil
	}
	lines := make([]string, 0, len(found))
	hints := make([]string, 0, len(found))
	for _, f := range found {
		var e *errs.Error
		if errors.As(f, &e) {
			lines = append(lines, e.Message)
			if e.Hint != "" {
				hints = append(hints, e.Hint)
			}
			continue
		}
		lines = append(lines, f.Error())
	}
	return errs.New(errs.LeakBlocked, strings.Join(lines, "; ")).WithHint(strings.Join(hints, "; "))
}

// hookIndexEnv passes on the index git gave this hook. `git commit -a` and
// `git commit -- <paths>` run pre-commit with GIT_INDEX_FILE set to a
// temporary index (the real one is locked), and gitref drops inherited
// repo-locating variables, so without this the stage step hits the locked
// main index and fails (exit 128). A relative value (git passes
// ".git/index" for a plain commit) is relative to the directory git ran
// the hook in, which is the worktree top, not the hook's working directory:
// the hook block of a project in a sub-folder cds into that folder first,
// and resolving from there named "<sub>/.git/index", which does not exist,
// so every commit in a monorepo with a sub-project failed with E_INTERNAL
// "pre-commit: stage .lore/data". The top is asked of git from root; if
// that fails, the hook's working directory is the fallback.
func hookIndexEnv(ctx context.Context, repo *gitref.Repo, root string) []string {
	idx := os.Getenv("GIT_INDEX_FILE")
	if idx == "" {
		return nil
	}
	if !filepath.IsAbs(idx) {
		if top, err := repo.Run(ctx, gitref.Invocation{Dir: root, Args: []string{"rev-parse", "--show-toplevel"}}); err == nil && strings.TrimSpace(string(top)) != "" {
			idx = filepath.Join(strings.TrimSpace(string(top)), idx)
		} else if abs, aerr := filepath.Abs(idx); aerr == nil {
			idx = abs
		}
	}
	return []string{"GIT_INDEX_FILE=" + idx}
}
