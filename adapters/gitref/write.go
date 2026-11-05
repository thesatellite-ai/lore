package gitref

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Changes is what a new commit changes relative to its base tree.
type Changes struct {
	// Put writes these files (root-relative path → full content), adding
	// or replacing them.
	Put map[string][]byte
	// Delete removes these files. Deleting a path the base does not hold is
	// a no-op, so landing a tombstone twice is harmless.
	Delete []string
}

// Empty reports whether the changes change nothing.
func (c Changes) Empty() bool { return len(c.Put) == 0 && len(c.Delete) == 0 }

// Signature is the author and committer of a commit. It is always explicit:
// a bare store clone usually has no user.name, and lore writes as the org
// identity, never as whatever git config the machine happens to hold.
type Signature struct {
	Name  string
	Email string
	// When is the commit time (from the caller's Clock).
	When time.Time
}

// History says how a new commit relates to its base.
type History int

const (
	// HistoryAppend makes the base commit the new commit's parent (no
	// parent when there is no base). Live rows, store branches, landings.
	HistoryAppend History = iota
	// HistoryReplace makes a parentless commit holding the base's tree plus
	// the changes, so the ref never accumulates history: signals and live
	// compaction (fixed rule 10, F8).
	HistoryReplace
)

// CommitRequest describes a commit to build without touching any work tree,
// index or ref.
type CommitRequest struct {
	// Base is the commit (any revision) whose tree the new tree starts
	// from; "" starts from the empty tree.
	Base string
	// History picks parent handling (default HistoryAppend).
	History History
	// Changes are applied to the base tree.
	Changes Changes
	// MergeParents are extra parents after Base, making a merge commit (a
	// local live ref merged with its tracking ref, F3 step 3). Only with
	// HistoryAppend and a Base. A merge commit is made even when the tree
	// equals Base's, because recording the other line is the point: without
	// it the next push would not fast-forward.
	MergeParents []string
	// Message is the commit message.
	Message string
	// Signature is the author and committer.
	Signature Signature
}

// ErrBadParents means MergeParents was given without a Base, with
// HistoryReplace, or names a missing commit.
var ErrBadParents = errors.New("gitref: merge parents need a base and HistoryAppend")

// Commit is the outcome of MakeCommit.
type Commit struct {
	// OID is the new commit, or the base commit when Changed is false
	// ("" when there was no base either).
	OID string
	// Tree is the commit's tree.
	Tree string
	// Base is the resolved base commit ("" when none).
	Base string
	// Changed is false when the changes left the base tree as it was: no
	// commit was made (C9), so writing the same row twice creates nothing.
	Changed bool
}

// ErrNoSignature refuses a commit without an author: see Signature.
var ErrNoSignature = errors.New("gitref: commit needs a signature (name and email)")

// ErrPathClash means a put would turn a file into a folder or a folder into
// a file. Git's index would silently drop the other side, losing rows.
var ErrPathClash = errors.New("gitref: path is both a file and a folder")

// MakeCommit builds a commit from req and returns it without moving any ref.
// Use it directly to build on a tracking ref and push the result elsewhere
// (a landing on origin/main never touches local branches, C6); use WriteRef
// to also move a local ref.
//
// Works in bare repositories: it uses a private temporary index
// (GIT_INDEX_FILE) and never reads a work tree.
func (r *Repo) MakeCommit(ctx context.Context, req CommitRequest) (Commit, error) {
	if req.Signature.Name == "" || req.Signature.Email == "" {
		return Commit{}, ErrNoSignature
	}
	if len(req.MergeParents) > 0 && (req.Base == "" || req.History != HistoryAppend) {
		return Commit{}, ErrBadParents
	}
	merges := make([]string, 0, len(req.MergeParents))
	for _, mp := range req.MergeParents {
		oid, ok, err := r.ResolveCommit(ctx, mp)
		if err != nil {
			return Commit{}, err
		}
		if !ok {
			return Commit{}, fmt.Errorf("%w: %s not found", ErrBadParents, mp)
		}
		merges = append(merges, oid)
	}
	var base, baseTree string
	if req.Base != "" {
		oid, ok, err := r.ResolveCommit(ctx, req.Base)
		if err != nil {
			return Commit{}, err
		}
		if !ok {
			return Commit{}, fmt.Errorf("%w: base %s", ErrNotFound, req.Base)
		}
		base = oid
		if baseTree, err = r.TreeOf(ctx, base); err != nil {
			return Commit{}, err
		}
	}
	tree, err := r.buildTree(ctx, base, req.Changes)
	if err != nil {
		return Commit{}, err
	}
	if base == "" && req.Changes.Empty() {
		return Commit{Tree: tree}, nil
	}
	if tree == baseTree && len(merges) == 0 {
		return Commit{OID: base, Tree: tree, Base: base}, nil
	}
	args := []string{"commit-tree", tree}
	if base != "" && req.History == HistoryAppend {
		args = append(args, "-p", base)
		for _, m := range merges {
			args = append(args, "-p", m)
		}
	}
	args = append(args, "-F", "-")
	oid, err := r.run(ctx, signatureEnv(req.Signature), []byte(req.Message), args...)
	if err != nil {
		return Commit{}, err
	}
	return Commit{OID: oid, Tree: tree, Base: base, Changed: true}, nil
}

// signatureEnv pins author and committer to sig. Git's raw date format
// "<unix seconds> <+hhmm>" keeps the caller's instant and zone exactly.
func signatureEnv(sig Signature) []string {
	date := strconv.FormatInt(sig.When.Unix(), 10) + " " + sig.When.Format("-0700")
	return []string{
		"GIT_AUTHOR_NAME=" + sig.Name, "GIT_AUTHOR_EMAIL=" + sig.Email, "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=" + sig.Name, "GIT_COMMITTER_EMAIL=" + sig.Email, "GIT_COMMITTER_DATE=" + date,
	}
}

// indexFileName is the temporary index's name inside its private folder.
const indexFileName = "index"

// blobFilePrefix names the temporary files blob contents are staged in for
// one `hash-object --stdin-paths` run.
const blobFilePrefix = "blob-"

// buildTree applies ch to base's tree (empty when base is "") in a private
// index and returns the written tree id.
func (r *Repo) buildTree(ctx context.Context, base string, ch Changes) (string, error) {
	puts, dels, err := normalizeChanges(ch)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "gitref-")
	if err != nil {
		return "", fmt.Errorf("gitref: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }() // best-effort cleanup of a private temp dir
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, indexFileName)}

	readArgs := []string{"read-tree", "--empty"}
	var existing []string
	if base != "" {
		readArgs = []string{"read-tree", base}
		out, err := r.run(ctx, nil, nil, "ls-tree", "-r", "-z", "--name-only", "--full-tree", base)
		if err != nil {
			return "", err
		}
		for p := range strings.SplitSeq(out, "\x00") {
			if p != "" {
				existing = append(existing, p)
			}
		}
	}
	if err := checkClashes(existing, puts, dels); err != nil {
		return "", err
	}
	if _, err := r.run(ctx, env, nil, readArgs...); err != nil {
		return "", err
	}

	var info bytes.Buffer
	if len(puts) > 0 {
		paths := make([]string, 0, len(puts))
		for p := range puts {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		var list bytes.Buffer
		for i, p := range paths {
			f := filepath.Join(tmp, blobFilePrefix+strconv.Itoa(i))
			if err := os.WriteFile(f, puts[p], blobFileMode); err != nil {
				return "", fmt.Errorf("gitref: stage blob: %w", err)
			}
			list.WriteString(f)
			list.WriteByte('\n')
		}
		out, err := r.run(ctx, nil, list.Bytes(), "hash-object", "-w", "--no-filters", "--stdin-paths")
		if err != nil {
			return "", err
		}
		oids := strings.Fields(out)
		if len(oids) != len(paths) {
			return "", fmt.Errorf("gitref: hash-object returned %d ids for %d files", len(oids), len(paths))
		}
		for i, p := range paths {
			fmt.Fprintf(&info, "%s %s\t%s\n", modeRegular, oids[i], p)
		}
	}
	for _, p := range dels {
		// Mode 0 removes the entry; a path not in the index is ignored.
		fmt.Fprintf(&info, "0 %s\t%s\n", r.zero, p)
	}
	if info.Len() > 0 {
		if _, err := r.run(ctx, env, info.Bytes(), "update-index", "--index-info"); err != nil {
			return "", err
		}
	}
	return r.run(ctx, env, nil, "write-tree")
}

// blobFileMode is the permission of staged blob files: private, since they
// may hold rows that are not yet shared.
const blobFileMode = 0o600

// normalizeChanges validates and cleans every path, and refuses a path that
// is both put and deleted (the caller's intent would be ambiguous).
func normalizeChanges(ch Changes) (map[string][]byte, []string, error) {
	puts := make(map[string][]byte, len(ch.Put))
	for p, b := range ch.Put {
		clean, err := cleanPath(p)
		if err != nil {
			return nil, nil, err
		}
		if _, dup := puts[clean]; dup {
			return nil, nil, fmt.Errorf("%w: %q given twice", ErrBadPath, clean)
		}
		puts[clean] = b
	}
	dels := make([]string, 0, len(ch.Delete))
	seen := map[string]bool{}
	for _, p := range ch.Delete {
		clean, err := cleanPath(p)
		if err != nil {
			return nil, nil, err
		}
		if _, both := puts[clean]; both {
			return nil, nil, fmt.Errorf("%w: %q is both written and deleted", ErrBadPath, clean)
		}
		if !seen[clean] {
			seen[clean] = true
			dels = append(dels, clean)
		}
	}
	sort.Strings(dels)
	return puts, dels, nil
}

// checkClashes refuses changes after which some path would be both a file
// and a folder (ErrPathClash).
func checkClashes(existing []string, puts map[string][]byte, dels []string) error {
	final := make(map[string]bool, len(existing)+len(puts))
	for _, p := range existing {
		final[p] = true
	}
	for _, p := range dels {
		delete(final, p)
	}
	for p := range puts {
		final[p] = true
	}
	dirs := map[string]bool{}
	for p := range final {
		for i := strings.IndexByte(p, '/'); i >= 0; i = nextSlash(p, i) {
			dirs[p[:i]] = true
		}
	}
	for p := range final {
		if dirs[p] {
			return fmt.Errorf("%w: %q", ErrPathClash, p)
		}
	}
	return nil
}

// nextSlash returns the index of the next '/' in p after i, or -1.
func nextSlash(p string, i int) int {
	j := strings.IndexByte(p[i+1:], '/')
	if j < 0 {
		return -1
	}
	return i + 1 + j
}

// CASError is a compare-and-swap ref update that lost: the ref was not at
// Expected when git tried to move it. Errors.Is(err, ErrRefMoved) is true.
type CASError struct {
	// Ref is the ref that was to be moved.
	Ref string
	// Expected is the commit the caller saw ("" = it should not exist).
	Expected string
	// Actual is where the ref is now ("" = it does not exist).
	Actual string
}

// Error describes the race.
func (e *CASError) Error() string {
	show := func(s string) string {
		if s == "" {
			return "(missing)"
		}
		return s
	}
	return fmt.Sprintf("gitref: %s moved: expected %s, found %s", e.Ref, show(e.Expected), show(e.Actual))
}

// Unwrap makes errors.Is(err, ErrRefMoved) work.
func (e *CASError) Unwrap() error { return ErrRefMoved }

// UpdateRef moves ref to newOID only if it currently points at oldOID
// (oldOID "" = only if the ref does not exist yet). Losing that race is a
// *CASError and leaves the ref untouched. A branch checked out in any work
// tree of the repository is refused (ErrCheckedOut, C6).
func (r *Repo) UpdateRef(ctx context.Context, ref, newOID, oldOID string) error {
	if newOID == "" {
		return fmt.Errorf("gitref: UpdateRef %s: empty new id (use DeleteRef)", ref)
	}
	return r.casRef(ctx, ref, newOID, oldOID)
}

// DeleteRef removes ref only if it points at oldOID (which must be set: a
// blind delete could drop commits nobody has seen). Same refusals as
// UpdateRef.
func (r *Repo) DeleteRef(ctx context.Context, ref, oldOID string) error {
	if oldOID == "" {
		return fmt.Errorf("gitref: DeleteRef %s: the expected old id is required", ref)
	}
	return r.casRef(ctx, ref, "", oldOID)
}

// casRef is the shared body of UpdateRef (newOID set) and DeleteRef (newOID
// "").
func (r *Repo) casRef(ctx context.Context, ref, newOID, oldOID string) error {
	if err := r.ValidateRef(ctx, ref); err != nil {
		return err
	}
	if err := r.refuseCheckedOut(ctx, ref); err != nil {
		return err
	}
	expect := oldOID
	if expect == "" {
		expect = r.zero
	}
	args := []string{"update-ref", "-m", reflogMessage, ref, newOID, expect}
	if newOID == "" {
		args = []string{"update-ref", "-m", reflogMessage, "-d", ref, expect}
	}
	if _, err := r.run(ctx, nil, nil, args...); err != nil {
		// Tell a lost race from any other failure by looking again: the
		// race is the case where the ref is no longer where we expected.
		actual, _, rerr := r.ResolveRef(ctx, ref)
		if rerr == nil && actual != oldOID {
			return &CASError{Ref: ref, Expected: oldOID, Actual: actual}
		}
		return err
	}
	return nil
}

// reflogMessage marks lore's ref moves in the reflog, so `git reflog` shows
// who moved a ref.
const reflogMessage = "lore: gitref update"

// refuseCheckedOut returns ErrCheckedOut when ref is a branch some work tree
// of this repository has checked out (the main one or a linked worktree).
// A bare repository's HEAD is not a checkout.
func (r *Repo) refuseCheckedOut(ctx context.Context, ref string) error {
	if !strings.HasPrefix(ref, headsPrefix) {
		return nil
	}
	out, err := r.run(ctx, nil, nil, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return err
	}
	var wt string
	bare := false
	for field := range strings.SplitSeq(out, "\x00") {
		switch {
		case field == "":
			wt, bare = "", false // end of one worktree record
		case strings.HasPrefix(field, "worktree "):
			wt = strings.TrimPrefix(field, "worktree ")
		case field == "bare":
			bare = true
		case strings.HasPrefix(field, "branch ") && !bare:
			if strings.TrimPrefix(field, "branch ") == ref {
				return fmt.Errorf("%w: %s is checked out in %s", ErrCheckedOut, ref, wt)
			}
		}
	}
	return nil
}

// WriteRequest is one write to a local ref.
type WriteRequest struct {
	// Ref is the full ref to move (refs/lore/live, refs/heads/main in a
	// store clone, …). A missing ref is created with a parentless commit.
	Ref string
	// History is HistoryAppend (keep history) or HistoryReplace (always one
	// parentless commit).
	History History
	// Changes are applied to the ref's current tree.
	Changes Changes
	// Message is the commit message.
	Message string
	// Signature is the author and committer.
	Signature Signature
}

// WriteResult is the outcome of WriteRef.
type WriteResult struct {
	// Old is where the ref was ("" = it did not exist).
	Old string
	// New is where the ref is now (Old when nothing changed).
	New string
	// Changed is false when the tree was unchanged: no commit, no ref move.
	Changed bool
}

// WriteRef reads ref's tip, builds a commit with the changes on it and moves
// the ref with compare-and-swap. If anything moved the ref in between, it
// returns a *CASError and the ref keeps the other writer's commit: the
// caller re-reads, re-decides and tries again — nothing is overwritten.
func (r *Repo) WriteRef(ctx context.Context, req WriteRequest) (WriteResult, error) {
	old, _, err := r.ResolveRef(ctx, req.Ref)
	if err != nil {
		return WriteResult{}, err
	}
	if rerr := r.refuseCheckedOut(ctx, req.Ref); rerr != nil {
		return WriteResult{}, rerr
	}
	c, err := r.MakeCommit(ctx, CommitRequest{Base: old, History: req.History, Changes: req.Changes, Message: req.Message, Signature: req.Signature})
	if err != nil {
		return WriteResult{}, err
	}
	if !c.Changed {
		return WriteResult{Old: old, New: old}, nil
	}
	if err := r.UpdateRef(ctx, req.Ref, c.OID, old); err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Old: old, New: c.OID, Changed: true}, nil
}
