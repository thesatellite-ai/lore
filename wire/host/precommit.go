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
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/dataignore"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/logic/merge3"
	"github.com/thesatellite-ai/lore/logic/validate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// Reasons a changed lore file is refused (printed after its path).
const (
	brokenConflictMarkers = "conflict markers"
	// brokenConflictNext follows the markers reason (H-4): they are an
	// unresolved git merge, not a typo in the JSON
	brokenConflictNext = ": an unresolved git merge conflict; lore sync conflicts lists it, keep one side and delete the marker lines"
	brokenInvalidJSON  = "invalid JSON"
	brokenMetaConflict = "unsettled metadata conflict in "
	brokenMetaDeleted  = "deleted; it holds the project's id and format, put it back: git checkout HEAD -- "
)

// metaFilePath is the project marker, relative to the project root.
var metaFilePath = loreDataPathspec + "/" + rowfs.MetaFileName

// migrationLeftOut is the line of a commit made while a `lore fix` format
// migration is still uncommitted: its files are not staged with it.
const migrationLeftOut = "lore: %d row files rewritten by lore fix (format migration) are not added to this commit; commit them on their own: git add .lore/data && git commit -m \"lore: migrate format\""

// brokenFilesHint is the next step of a refused pre-commit.
const brokenFilesHint = "fix the file by hand (a row file is one JSON object named after its own id; settle a conflict with the row's edit command), or restore it with `git checkout HEAD -- <path>`; then commit again"

// loreDataPathspec is the project's row folder, relative to its root.
var loreDataPathspec = filepath.ToSlash(filepath.Join(rowfs.LoreDir, rowfs.DataDir))

// preCommit is `lore sync hook pre-commit` (see the file comment).
func (h *liveHost) preCommit(ctx context.Context) error {
	if top, isGit, inStore := rowfs.StoreTop(h.cfg.WorkDir); inStore && isGit {
		return h.storePreCommit(ctx, top) // a hand commit in a store clone (precommit_store.go)
	}
	root := liveProjectRoot(h.cfg.WorkDir)
	if root == "" {
		// not a lore project here, never block git over it, except for a
		// commit that deletes the project itself (H-6: rm -rf .lore and
		// git commit -a dropped every row file without a word)
		return h.deletedProject(ctx)
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
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return errs.Wrap(errs.Internal, "pre-commit: core registry", err)
	}
	var bad []string
	for _, p := range changed {
		full := filepath.Clean(filepath.Join(root, filepath.FromSlash(p))) // p is a path git listed under .lore/data
		b, rerr := os.ReadFile(full)
		if errors.Is(rerr, os.ErrNotExist) {
			if p == metaFilePath {
				// H-3: the project marker (format and project id) is
				// tracked and only ever rewritten, never deleted
				bad = append(bad, p+" ("+brokenMetaDeleted+p+")")
			}
			continue // a deletion of a row file: staging records it
		}
		if rerr != nil {
			bad = append(bad, p+" ("+unreadableWhy(full, rerr)+")") // H-2: was E_INTERNAL "pre-commit: read <path>"
			continue
		}
		if row.HasBOM(b) {
			// I-15: an editor's byte order mark; strip it so the committed
			// file is plain UTF-8 every lore (and every older one) reads
			b = row.TrimBOM(b)
			if werr := os.WriteFile(full, b, 0o644); werr != nil { // a tracked row file
				bad = append(bad, p+" (starts with a UTF-8 byte order mark that could not be removed: "+werr.Error()+")")
				continue
			}
			h.line("lore: removed the UTF-8 byte order mark from " + p + " (lore row files are plain UTF-8)")
		}
		if why := brokenLoreFile(b); why != "" {
			bad = append(bad, p+" ("+why+")")
		} else if why := wrongRowID(root, p, b); why != "" {
			bad = append(bad, p+" ("+why+")") // H-6: reads leave such a file out on every clone
		} else if line, why := invalidRepoRow(reg, p, b, headVersion(ctx, repo, root, p)); why != "" {
			bad = append(bad, p+":"+strconv.Itoa(line)+" ("+why+")") // D-18: what the hand-edit pick-up refused
		}
	}
	if len(bad) > 0 {
		// one line per file (H-6), so a long list stays readable
		return errs.New(errs.InvalidInput, "refusing to commit broken lore files:\n  "+strings.Join(bad, "\n  ")).WithHint(brokenFilesHint)
	}
	if len(changed) > 0 {
		before := wire.HookIndexBegin(ctx, root) // the index as the person staged it
		staged := indexEntries(ctx, repo, root)  // L-3: what the person staged themselves
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
		if line := stagedLine(staged, indexEntries(ctx, repo, root)); line != "" {
			h.line(line)
		}
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

// unreadableWhy is why the changed file full cannot be committed when
// reading it failed: permission denied names the chmod that fixes it.
func unreadableWhy(full string, err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "cannot be read: permission denied; make it readable again: chmod u+rw " + full
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return "cannot be read: " + pe.Err.Error()
	}
	return "cannot be read: " + err.Error()
}

// wrongRowID is why the row file p (relative to root, under .lore/data)
// cannot be committed although it is one JSON object: the row it holds is
// not the one its file name says, so every read on every clone leaves it
// out; when the file named after the id it holds exists too, two files
// claim one id. "" = fine, or not a row file.
func wrongRowID(root, p string, b []byte) string {
	rel, ok := strings.CutPrefix(p, loreDataPathspec+"/")
	if !ok {
		return ""
	}
	table, id, isRow := rowfs.ParseRowPath(rel)
	if !isRow {
		return ""
	}
	r, err := row.Decode(b)
	if err != nil || (r.Table == table && r.ID == id) {
		return ""
	}
	why := "holds the row " + r.ID.String() + " of table " + string(r.Table) + ", but its file name says " + id.String() + " of table " + string(table)
	other := loreDataPathspec + "/" + rowfs.RowPath(r.Table, r.ID)
	if _, serr := os.Lstat(filepath.Join(root, filepath.FromSlash(other))); serr == nil {
		why += "; " + other + " holds that id too (a duplicate id)"
	}
	return why
}

// deletedProject refuses a commit that deletes a project's .lore folder
// (its _meta.json marker is staged as deleted against HEAD) from the hook
// of a checkout where .lore is gone: one line naming the folder and how
// to put it back. Anything git cannot answer here never blocks the commit.
func (h *liveHost) deletedProject(ctx context.Context) error {
	repo, err := gitref.Open(ctx, h.cfg.WorkDir, h.cfg.Git)
	if err != nil {
		return nil
	}
	out, err := repo.Run(ctx, gitref.Invocation{Dir: h.cfg.WorkDir, Env: hookIndexEnv(ctx, repo, h.cfg.WorkDir),
		Args: []string{"-c", "core.quotePath=false", "diff", "--cached", "--name-only", "--diff-filter=D", "-z", "--relative", "HEAD", "--"}})
	if err != nil {
		return nil // no HEAD yet: nothing committed to delete
	}
	var gone []string
	for _, p := range strings.Split(string(out), "\x00") {
		if dir, ok := strings.CutSuffix(p, metaFilePath); ok && (dir == "" || strings.HasSuffix(dir, "/")) {
			gone = append(gone, dir+rowfs.LoreDir)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	return errs.New(errs.InvalidInput, "refusing to commit the deletion of the lore project folder "+strings.Join(gone, ", ")+" (every rule, memory and task in it)").
		WithHint("put it back: git checkout HEAD -- " + strings.Join(gone, " ") + "; to take lore out of this repo on purpose, commit with git commit --no-verify")
}

// headVersion is HEAD's copy of the file p (relative to root), nil when
// HEAD has none (a new file, a first commit).
func headVersion(ctx context.Context, repo *gitref.Repo, root, p string) []byte {
	out, err := repo.Run(ctx, gitref.Invocation{Dir: root, Args: []string{"show", "HEAD:./" + p}})
	if err != nil {
		return nil
	}
	return out
}

// invalidRepoRow is why a hand-edited row file of the code repo (p relative
// to the project root; head its committed copy) cannot be committed although
// it is valid JSON: a changed field that is unknown or holds a value outside
// its kind or set, what the hand-edit pick-up refuses it for (D-18), with
// the line ("" = fine). Like the pick-up, only a hand edit counts: the file
// keeps HEAD's _hlc (lore restamps every row it writes), HEAD's copy is on
// the current format, and only the changed fields are checked.
func invalidRepoRow(reg *schema.Registry, p string, b, head []byte) (int, string) {
	rel, ok := strings.CutPrefix(p, loreDataPathspec+"/")
	if !ok || head == nil {
		return 0, ""
	}
	table, _, isRow := rowfs.ParseRowPath(rel)
	if !isRow {
		return 0, ""
	}
	def, known := reg.Table(table)
	r, err := row.Decode(b)
	base, berr := row.Decode(head)
	if !known || err != nil || berr != nil || r.Deleted || r.HLC != base.HLC || base.SourceVersion != row.FormatVersion {
		return 0, "" // not a hand edit lore can check here: the pick-up says why
	}
	changed := map[string]value.Value{}
	for _, name := range engine.PickUpChanged(base, r) {
		if v, has := r.Fields[name]; has {
			changed[name] = v
		}
	}
	if len(changed) == 0 {
		return 0, ""
	}
	if _, verr := validate.Fields(def, engine.RetypeFields(def, changed), validate.Patch); verr != nil {
		msg := verr.Error()
		var e *errs.Error
		if errors.As(verr, &e) {
			msg = e.Message
		}
		// the line of the first field the message names as a problem
		// ("field <name>:" or "unknown field <name> (…)"), never one of
		// the allowed names it lists
		line := 0
		for name := range changed {
			if strings.Contains(msg, "field "+name+":") || strings.Contains(msg, "field "+name+" ") {
				if l := keyLine(b, name); line == 0 || l < line {
					line = l
				}
			}
		}
		return max(line, 1), msg
	}
	return 0, ""
}

// brokenLoreText is brokenLoreFile's byte-level half: markers or not JSON.
func brokenLoreText(b []byte) string {
	if merge3.ContainsConflictMarkers(string(b)) {
		return brokenConflictMarkers + brokenConflictNext
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
			return brokenConflictMarkers + " in a value" + brokenConflictNext
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

// indexEntries maps each index path under .lore/data to its staged entry
// ("<mode> <oid> <stage>"), read through the hook's index (nil when git
// cannot be read: then nothing is reported).
func indexEntries(ctx context.Context, repo *gitref.Repo, root string) map[string]string {
	out, err := repo.Run(ctx, gitref.Invocation{Dir: root, Args: []string{"-c", "core.quotePath=false", "ls-files", "-s", "-z", "--", loreDataPathspec}, Env: hookIndexEnv(ctx, repo, root)})
	if err != nil {
		return nil
	}
	m := map[string]string{}
	for _, e := range strings.Split(string(out), "\x00") {
		meta, p, ok := strings.Cut(e, "\t")
		if ok {
			m[p] = meta
		}
	}
	return m
}

// stagedListMax is how many paths the staged line names before "and N more".
const stagedListMax = 5

// stagedLine is the pre-commit hook's one line naming the lore row files it
// staged into the person's commit (L-3, B1-20): every path whose index
// entry the staging added, changed or removed. "" when it changed none, or
// when the index could not be read before or after.
func stagedLine(before, after map[string]string) string {
	if before == nil || after == nil {
		return ""
	}
	var paths []string
	for p, e := range after {
		if before[p] != e {
			paths = append(paths, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return ""
	}
	sort.Strings(paths)
	shown := paths
	more := ""
	if len(shown) > stagedListMax {
		shown, more = shown[:stagedListMax], fmt.Sprintf(" and %d more", len(paths)-stagedListMax)
	}
	return fmt.Sprintf("lore: added %d lore row file(s) to this commit: %s%s (lore's rows travel with your commits)", len(paths), strings.Join(shown, ", "), more)
}

// CommitRender renders the project at root for the commit being made and
// returns the render files (relative to root) to stage with it; nil stages
// nothing. cmd/lore passes renderhost.Host.CommitRender.
type CommitRender func(ctx context.Context, root string) []string

// commitRenderLine is the pre-commit line when the commit now carries a
// fresh render of the rows it adds.
const commitRenderLine = "lore: refreshed .lore/LORE.md in this commit for the row files it carries"

// stageCommitRender is the pre-commit hook's second step (A-12): when the
// commit carries row files, the committed render is refreshed and staged
// with them, so the next checkout of this commit has nothing to re-render
// and no hook leaves a tracked LORE.md dirty. A store clone, a commit
// without row files and any git problem stage nothing; it never refuses
// the commit.
func (h *liveHost) stageCommitRender(ctx context.Context, render CommitRender) {
	if render == nil {
		return
	}
	if _, isGit, inStore := rowfs.StoreTop(h.cfg.WorkDir); inStore && isGit {
		return
	}
	root := liveProjectRoot(h.cfg.WorkDir)
	if root == "" {
		return
	}
	repo, err := gitref.Open(ctx, root, h.cfg.Git)
	if err != nil {
		return
	}
	env := hookIndexEnv(ctx, repo, root)
	if !stagedDiffers(ctx, repo, root, env, loreDataPathspec) {
		return // no row file in this commit: its render is the one HEAD has
	}
	files := render(ctx, root)
	if len(files) == 0 {
		return
	}
	if _, err := repo.Run(ctx, gitref.Invocation{Dir: root, Env: env, Args: append([]string{"add", "--"}, files...)}); err != nil {
		h.line("lore: pre-commit: stage " + strings.Join(files, " and ") + ": " + err.Error())
		return
	}
	if stagedDiffers(ctx, repo, root, env, files...) {
		h.line(commitRenderLine)
	}
}

// stagedDiffers reports whether the index being committed differs from
// HEAD under paths (true with no HEAD yet: a first commit carries all).
func stagedDiffers(ctx context.Context, repo *gitref.Repo, root string, env []string, paths ...string) bool {
	if _, err := repo.Run(ctx, gitref.Invocation{Dir: root, Args: []string{"rev-parse", "--verify", "-q", "HEAD"}}); err != nil {
		return true
	}
	_, err := repo.Run(ctx, gitref.Invocation{Dir: root, Env: env, Args: append([]string{"diff", "--cached", "--quiet", "HEAD", "--"}, paths...)})
	return err != nil
}
