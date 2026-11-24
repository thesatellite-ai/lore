package reposync

// branch.go — `lore sync peek` and `lore sync promote` (LORE_TEAM_SPEC
// command table; v0.1.17 saas/cmd/cli sync_host.go): read the lore rows of
// another git ref without checking it out, and commit row files onto
// another local branch without touching the work tree.
//
//	peek <ref> [table] ─▶ ls-tree + cat-file of <project>/.lore/data at ref
//	                     ─▶ one line per row: table, id, summary
//	promote <id>… --to <branch> ─▶ the row files of this checkout, each
//	                        merged field by field over <branch>'s copy
//	                        against git merge-base(HEAD, <branch>) (H-207)
//	                     ─▶ a new commit on refs/heads/<branch> (CAS, never
//	                        the checked-out branch), nothing pushed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
)

// summaryKeys are the row fields a peek line shows, first present wins.
var summaryKeys = []string{"title", "name", "body", "stable_key", "key"}

// peekSummaryLen bounds the one-line summary peek prints per row.
const peekSummaryLen = 100

// promoteSHALen is how much of the promote commit id is printed.
const promoteSHALen = 12

// PeekRow is one row read from another ref.
type PeekRow struct {
	Table   string `json:"table"`
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

// Peek lists the rows of the lore project at dir as ref holds them (table
// "" = every table), sorted by table then id. The work tree, index and
// HEAD are not touched.
func Peek(ctx context.Context, dir, ref, table string) ([]PeekRow, error) {
	repo, top, err := openRepo(ctx, dir)
	if err != nil {
		return nil, err
	}
	dir = projectDir(top, dir) // E-FATES-6
	base := PathPrefix(top, dir) + dataRel
	prefix := base
	if table != "" {
		prefix += "/" + table
	}
	files, err := repo.ReadTree(ctx, ref, prefix)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "read "+ref, err).
			WithHint("is it a valid ref? try `git fetch` first for remote branches")
	}
	rows := []PeekRow{}
	for p, b := range files {
		rel := strings.TrimPrefix(p, base+"/")
		tbl, file := path.Split(rel)
		if tbl == "" {
			continue // _meta.json / _purged.json
		}
		tbl = strings.TrimSuffix(tbl, "/")
		id, summary, derr := rowSummary(b)
		if derr != nil {
			rows = append(rows, PeekRow{Table: tbl, ID: strings.TrimSuffix(file, ".json"), Summary: "(unreadable: " + derr.Error() + ")"})
			continue
		}
		rows = append(rows, PeekRow{Table: tbl, ID: id, Summary: summary})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Table != rows[j].Table {
			return rows[i].Table < rows[j].Table
		}
		return rows[i].ID < rows[j].ID
	})
	return rows, nil
}

// PeekLines renders a peek for a terminal.
func PeekLines(ref string, rows []PeekRow) []string {
	if len(rows) == 0 {
		return []string{"· no lore rows at " + ref}
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, fmt.Sprintf("%-18s %s  %s", r.Table, r.ID, r.Summary))
	}
	return out
}

// rowSummary reads a row file's id and its first summary field's first
// line (bounded).
func rowSummary(b []byte) (id, summary string, err error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", "", err
	}
	str := func(k string) string {
		var s string
		if raw, ok := doc[k]; ok && json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	id = str("id")
	for _, k := range summaryKeys {
		if s := strings.TrimSpace(str(k)); s != "" {
			line := strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
			if len(line) > peekSummaryLen {
				line = line[:peekSummaryLen] + "…"
			}
			return id, line, nil
		}
	}
	return id, "", nil
}

// PromoteOptions are `lore sync promote`'s inputs.
type PromoteOptions struct {
	// Dir is the lore project folder.
	Dir string
	// IDs are the rows whose files are committed.
	IDs []string
	// Branch is the local target branch (never the checked-out one).
	Branch string
	// DryRun lists what would be committed.
	DryRun bool
	// Now is the commit time.
	Now time.Time
	// Merger merges a row both this checkout and Branch changed since
	// they split (wire passes the field merge every other landing uses);
	// nil merges nothing, so any such row clashes.
	Merger git.Merger
	// Take settles a row whose same text both sides changed: nil stops
	// the promote with E_LAND_CONFLICT, true keeps this checkout's text of
	// those fields, false Branch's; every other field still merges.
	Take *bool
}

// PromoteResult is what promote did.
type PromoteResult struct {
	// Paths are the repository-relative files committed (or that would be).
	Paths []string
	// Commit is the new commit (empty on a dry run).
	Commit string
	// Merged are the rows Branch also changed since the split, merged
	// field by field (Branch's edits kept).
	Merged []string
	// Taken are the rows whose clashing text Take settled.
	Taken []string
	// Same are the rows Branch already holds as merged (nothing to write).
	Same []string
}

// Promote commits the current row files of IDs onto Branch as a new commit
// (the branch's tree plus those files), with the person's git identity.
// Each file is merged over Branch's copy against the copy at git
// merge-base(HEAD, Branch), through the same row merge as lore branch
// promote (git.MergeRowOver): what only Branch changed since the two split
// stays (#155 class, H-207: an archive, metadata or a newer body on Branch
// was overwritten by this checkout's older copy). A text both changed
// refuses the whole promote with E_LAND_CONFLICT, nothing written, unless
// Take chose a side. Nothing is pushed; the checked-out branch is refused
// (edit and commit normally there).
func Promote(ctx context.Context, o PromoteOptions) (PromoteResult, error) {
	var res PromoteResult
	if o.Branch == "" {
		return res, errs.New(errs.InvalidInput, "--to <branch> is required")
	}
	if len(o.IDs) == 0 {
		return res, errs.New(errs.InvalidInput, "name at least one row id")
	}
	repo, top, err := openRepo(ctx, o.Dir)
	if err != nil {
		return res, err
	}
	o.Dir = projectDir(top, o.Dir) // E-FATES-6
	dataDir := filepath.Join(o.Dir, filepath.FromSlash(dataRel))
	prefix := PathPrefix(top, o.Dir)
	ref := "refs/heads/" + o.Branch
	old, ok, err := repo.ResolveRef(ctx, ref)
	if err != nil {
		return res, errs.Wrap(errs.Internal, "resolve "+o.Branch, err)
	}
	if !ok {
		return res, errs.New(errs.NotFound, "branch "+o.Branch+" not found").WithHint("promote commits onto an existing local branch")
	}
	cur, cerr := run(ctx, o.Dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if cerr == nil && cur == o.Branch {
		return res, errs.New(errs.InvalidInput, "refusing to commit onto the checked-out branch "+o.Branch+"; edit the files and commit normally")
	}
	if cerr != nil || cur == "" {
		cur = "this checkout"
	}
	split, err := promoteBase(ctx, repo, old)
	if err != nil {
		return res, err
	}
	files := map[string][]byte{}
	var clashes []string
	for _, id := range o.IDs {
		rel, theirs, ferr := findRowFile(dataDir, id)
		if ferr != nil {
			return res, ferr
		}
		p := prefix + dataRel + "/" + rel
		ours, err := readAt(ctx, repo, old, p)
		if err != nil {
			return res, err
		}
		base, err := readAt(ctx, repo, split, p)
		if err != nil {
			return res, err
		}
		doc, how, fields, err := git.MergeRowOver(o.Merger, base, ours, theirs, o.Take)
		if err != nil {
			return res, errs.Prefix("merge "+id+" with "+o.Branch+"'s copy", err)
		}
		switch how {
		case git.RowMergeClash:
			n := id
			if len(fields) > 0 {
				n += " (" + strings.Join(fields, ", ") + ")"
			}
			clashes = append(clashes, n)
			continue
		case git.RowMergeSame:
			res.Same = append(res.Same, id)
			continue
		case git.RowMergeFields:
			res.Merged = append(res.Merged, id)
		case git.RowMergeTaken:
			res.Taken = append(res.Taken, id)
		}
		files[p] = doc
		res.Paths = append(res.Paths, p)
	}
	if len(clashes) > 0 {
		verb := "nothing was committed"
		if o.DryRun {
			verb = "the promote would stop here (dry run, nothing changed)"
		}
		return res, errs.New(errs.LandConflict, cur+" and "+o.Branch+" both changed the same text of "+strings.Join(clashes, ", ")+" since they split; "+verb).
			WithHint(syncPromoteClashHint(o))
	}
	if o.DryRun || len(files) == 0 {
		return res, nil
	}
	sig, ok := repo.GitIdentity(ctx)
	if !ok {
		return res, errs.New(errs.InvalidInput, "git has no user.name/user.email here").WithHint("set them with git config, then promote again")
	}
	sig.When = o.Now
	c, err := repo.MakeCommit(ctx, gitref.CommitRequest{
		Base: old, Changes: gitref.Changes{Put: files},
		Message: "lore: promote " + strings.Join(o.IDs, ", "), Signature: sig,
	})
	if err != nil {
		return res, errs.Wrap(errs.Internal, "promote", err)
	}
	if !c.Changed {
		return res, nil // the branch already holds these files
	}
	if err := repo.UpdateRef(ctx, ref, c.OID, old); err != nil {
		if errors.Is(err, gitref.ErrCheckedOut) {
			return res, errs.Wrap(errs.InvalidInput, o.Branch+" is checked out in another work tree", err)
		}
		return res, errs.Wrap(errs.Internal, "move "+o.Branch, err)
	}
	res.Commit = c.OID
	return res, nil
}

// promoteBase is the commit this checkout and the target tip split at:
// git merge-base(HEAD, tip); "" when they share no history (every row the
// target also holds then merges against no base).
func promoteBase(ctx context.Context, repo *gitref.Repo, tip string) (string, error) {
	head, ok, err := repo.ResolveCommit(ctx, "HEAD")
	if err != nil {
		return "", errs.Wrap(errs.Internal, "resolve HEAD", err)
	}
	if !ok {
		return "", nil // an unborn branch: nothing committed to split from
	}
	mb, ok, err := repo.MergeBase(ctx, head, tip)
	if err != nil {
		return "", errs.Wrap(errs.Internal, "merge-base HEAD "+tip, err)
	}
	if !ok {
		return "", nil
	}
	return mb, nil
}

// readAt is the file p at commit rev, nil when rev is "" or has no p.
func readAt(ctx context.Context, repo *gitref.Repo, rev, p string) ([]byte, error) {
	if rev == "" {
		return nil, nil
	}
	b, ok, err := repo.ReadFile(ctx, rev, p)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "read "+p+" at "+rev, err)
	}
	if !ok {
		return nil, nil
	}
	return b, nil
}

// syncPromoteClashHint names the two promotes that settle a clash, with
// every id and flag the person ran (named flags, real ids).
func syncPromoteClashHint(o PromoteOptions) string {
	// one --ids per row: this op's ids do not split on commas
	cmd := "lore sync promote --ids " + strings.Join(o.IDs, " --ids ") + " --to " + o.Branch
	return "keep one side's text of those fields (every other field still merges): " + cmd + " --take branch keeps this checkout's, " +
		cmd + " --take main keeps " + o.Branch + "'s; or edit the row on one side so both agree, then promote again"
}

// PromoteLines renders a promote for a terminal.
func PromoteLines(o PromoteOptions, res PromoteResult) []string {
	var out []string
	if o.DryRun {
		out = make([]string, 0, len(res.Paths)+1)
		for _, p := range res.Paths {
			out = append(out, fmt.Sprintf("would commit %s onto %s", p, o.Branch))
		}
	} else if res.Commit == "" {
		// the counts below still say why (H-207: a --take main that keeps
		// the target's copy as it is writes nothing)
		out = []string{fmt.Sprintf("· %s already holds these rows; nothing committed", o.Branch)}
	} else {
		out = []string{fmt.Sprintf("✓ committed %d file(s) onto %s (%s) — not pushed", len(res.Paths), o.Branch, res.Commit[:min(promoteSHALen, len(res.Commit))])}
	}
	if n := len(res.Merged) + len(res.Taken) + len(res.Same); n > 0 {
		out = append(out, fmt.Sprintf("  %d as this checkout has them, %d merged field by field with newer edits on %s (those edits kept), %d settled by --take, %d already the same",
			len(res.Paths)-len(res.Merged)-len(res.Taken), len(res.Merged), o.Branch, len(res.Taken), len(res.Same)))
	}
	return out
}

// rowIDShape reports <prefix>_<suffix> with a lower-case alphanumeric
// prefix and an alphanumeric suffix: the shape of every row file name, and
// never a path (no separator, no dot).
func rowIDShape(id string) bool {
	p, rest, ok := strings.Cut(id, "_")
	if !ok || p == "" || rest == "" {
		return false
	}
	for _, r := range p {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	for _, r := range rest {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// findRowFile locates <table>/<id>.json in the data folder.
func findRowFile(dataDir, id string) (string, []byte, error) {
	// Only a row id names a file: anything else (a typo, "../x") is
	// E_INVALID_ID before a path is built from it, so a value can never
	// read or promote a file outside .lore/data/<table>/.
	if !rowIDShape(id) {
		return "", nil, errs.New(errs.InvalidID, strconv.Quote(id)+" is not a row id").
			WithHint("promote takes row ids (tsk_…, rul_…) written to this checkout's .lore/data")
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return "", nil, errs.Wrap(errs.Internal, "read .lore/data", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rel := e.Name() + "/" + id + ".json"
		b, err := os.ReadFile(filepath.Clean(filepath.Join(dataDir, e.Name(), id+".json")))
		if err == nil {
			return rel, b, nil
		}
	}
	return "", nil, errs.New(errs.NotFound, "no .lore/data file for "+id).
		WithHint("promote takes rows written to this checkout's .lore/data")
}

// openRepo opens the git repository at dir and its top folder.
func openRepo(ctx context.Context, dir string) (*gitref.Repo, string, error) {
	top, err := toplevel(ctx, dir)
	if err != nil {
		return nil, "", errs.Wrap(errs.Unsupported, "this needs a git repository", err)
	}
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		return nil, "", errs.Wrap(errs.Unsupported, "this needs a git repository", err)
	}
	return repo, top, nil
}
