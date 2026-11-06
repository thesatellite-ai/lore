package reposync

// branch.go — `lore sync peek` and `lore sync promote` (LORE_TEAM_SPEC
// command table; v0.1.17 saas/cmd/cli sync_host.go): read the lore rows of
// another git ref without checking it out, and commit row files onto
// another local branch without touching the work tree.
//
//	peek <ref> [table] ─▶ ls-tree + cat-file of <project>/.lore/data at ref
//	                     ─▶ one line per row: table, id, summary
//	promote <id>… --to <branch> ─▶ the row files of this checkout
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
	"strings"
	"time"

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
}

// PromoteResult is what promote did.
type PromoteResult struct {
	// Paths are the repository-relative files committed (or that would be).
	Paths []string
	// Commit is the new commit (empty on a dry run).
	Commit string
}

// Promote commits the current row files of IDs onto Branch as a new commit
// (the branch's tree plus those files), with the person's git identity.
// Nothing is pushed; the checked-out branch is refused (edit and commit
// normally there).
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
	files := map[string][]byte{}
	for _, id := range o.IDs {
		rel, b, ferr := findRowFile(dataDir, id)
		if ferr != nil {
			return res, ferr
		}
		p := prefix + dataRel + "/" + rel
		files[p] = b
		res.Paths = append(res.Paths, p)
	}
	ref := "refs/heads/" + o.Branch
	old, ok, err := repo.ResolveRef(ctx, ref)
	if err != nil {
		return res, errs.Wrap(errs.Internal, "resolve "+o.Branch, err)
	}
	if !ok {
		return res, errs.New(errs.NotFound, "branch "+o.Branch+" not found").WithHint("promote commits onto an existing local branch")
	}
	if cur, cerr := run(ctx, o.Dir, "symbolic-ref", "--quiet", "--short", "HEAD"); cerr == nil && cur == o.Branch {
		return res, errs.New(errs.InvalidInput, "refusing to commit onto the checked-out branch "+o.Branch+"; edit the files and commit normally")
	}
	if o.DryRun {
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

// PromoteLines renders a promote for a terminal.
func PromoteLines(o PromoteOptions, res PromoteResult) []string {
	if o.DryRun {
		out := make([]string, 0, len(res.Paths))
		for _, p := range res.Paths {
			out = append(out, fmt.Sprintf("would commit %s onto %s", p, o.Branch))
		}
		return out
	}
	if res.Commit == "" {
		return []string{fmt.Sprintf("· %s already holds these rows; nothing committed", o.Branch)}
	}
	return []string{fmt.Sprintf("✓ committed %d file(s) onto %s (%s) — not pushed", len(res.Paths), o.Branch, res.Commit[:min(promoteSHALen, len(res.Commit))])}
}

// findRowFile locates <table>/<id>.json in the data folder.
func findRowFile(dataDir, id string) (string, []byte, error) {
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
