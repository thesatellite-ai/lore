package wire

// scopegone.go — folder scopes whose folder is gone from the checkout
// (B3-5, CONTEXT_BUDGET 6.2, D12: a scope's id is permanent, its path a
// label). After `git mv src/booking src/bookings` the scope dir:src/booking
// names no folder, so its rules stop applying to the moved files and render
// puts them in the root file; nothing said so. One detection serves every
// place that tells the person: lore doctor, lore render, the post-commit
// hook, lore sync (rename hints) and lore fix (which relabels the scope when
// git saw the rename).
//
//	repo scopes rows ─▶ path no longer a folder in the checkout?
//	  ─▶ FolderRenamedTo: git log -1 -- <path>/, then git show -M
//	     --diff-filter=R <that commit>; new folder = the renamed file's
//	     path minus its path inside the old folder (must exist now)
//	  ─▶ GoneScope{Path, RenamedTo} ─▶ Line(): one sentence with the command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsscope "github.com/thesatellite-ai/lore/ops/scope"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// renameStatusPrefix starts a rename line of `git show --name-status`.
const renameStatusPrefix = "R"

// GoneScope is one repo folder scope whose folder is not in the checkout.
type GoneScope struct {
	// ID is the scp_ row id.
	ID string
	// Path is the folder the scope names (relative to the repo top).
	Path string
	// RenamedTo is the folder git saw its files renamed into, "" when git
	// shows no such rename (the folder was deleted, never committed, or
	// moved outside git).
	RenamedTo string
}

// MoveArgs is the `lore scope move` argv (without "lore") that relabels g,
// with "<new-path>" when no rename was seen.
func (g GoneScope) MoveArgs() []string {
	to := g.RenamedTo
	if to == "" {
		to = "<new-path>"
	}
	return []string{"scope", "move", g.Path, to}
}

// Line is the one-line notice of g: what happened, what it means for the
// rules, and the command that keeps them on the folder.
func (g GoneScope) Line() string {
	cmd := "lore " + strings.Join(g.MoveArgs(), " ")
	if g.RenamedTo != "" {
		return fmt.Sprintf("folder %s was renamed to %s in git; its scoped rules no longer apply there (they render into the root file). Keep them on the folder: %s (or lore fix)", g.Path, g.RenamedTo, cmd)
	}
	return fmt.Sprintf("folder scope %s no longer exists in this checkout; its scoped rules render into the root file. If it moved: %s; if it is gone for good, archive its rules", g.Path, cmd)
}

// GoneScopes lists the repo's folder scopes whose folder is not in the
// checkout at top, each with the rename git saw, if any. It costs one
// listing of the scopes rows and a stat per scope; git runs only for a
// scope that is gone.
func (a *App) GoneScopes(ctx context.Context, top string) ([]GoneScope, error) {
	rows, err := a.List(ctx, store.SourceRepo, schema.TableScopes)
	if err != nil {
		return nil, err
	}
	return goneScopes(ctx, top, rows), nil
}

// goneScopes is GoneScopes over rows already read.
func goneScopes(ctx context.Context, top string, rows []row.Row) []GoneScope {
	var out []GoneScope
	for _, r := range rows {
		if r.Deleted {
			continue
		}
		v := r.Fields[schema.FieldScopePath]
		if v == nil {
			continue
		}
		p := strings.Trim(v.String(), "/")
		if p == "" || isDirAt(filepath.Join(top, filepath.FromSlash(p))) {
			continue
		}
		out = append(out, GoneScope{ID: r.ID.String(), Path: p, RenamedTo: FolderRenamedTo(ctx, top, p)})
	}
	return out
}

// FolderRenamedTo is the folder the files of old (a folder relative to
// top) moved to in the latest commit that renamed them, "" when none is
// found or that folder is gone too. Errors (no git, no commit) are "": the
// caller still reports the missing folder.
func FolderRenamedTo(ctx context.Context, top, old string) string {
	// A path-limited log cannot pair a rename (the new side is outside the
	// pathspec), so find the last commit that touched old/, then read that
	// whole commit with rename detection.
	sha, err := gitRun{dir: top, args: []string{"log", "-1", "--format=%H", "--", old + "/"}}.output(ctx)
	if err != nil || strings.TrimSpace(string(sha)) == "" {
		return ""
	}
	log, err := gitRun{dir: top, args: []string{"show", "--format=", "--name-status", "-M", "--diff-filter=R", strings.TrimSpace(string(sha))}}.output(ctx)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(log), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\t")
		if len(parts) != 3 || !strings.HasPrefix(parts[0], renameStatusPrefix) {
			continue
		}
		rel, ok := strings.CutPrefix(parts[1], old+"/")
		if !ok {
			continue
		}
		nd, ok := strings.CutSuffix(parts[2], "/"+rel)
		if ok && nd != old && isDirAt(filepath.Join(top, filepath.FromSlash(nd))) {
			return nd
		}
	}
	return ""
}

// HeadRemovedPaths are the paths the HEAD commit at top deleted or renamed
// away (the old side), "" entries never. The post-commit notice checks
// only scopes under these, so a commit that moved nothing costs one git
// call and no store open.
func HeadRemovedPaths(ctx context.Context, top string) []string {
	out, err := gitRun{dir: top, args: []string{"show", "--format=", "--name-status", "-M", "--diff-filter=DR", "HEAD"}}.output(ctx)
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\t")
		if len(parts) >= 2 && parts[1] != "" {
			paths = append(paths, parts[1])
		}
	}
	return paths
}

// isDirAt reports whether path is an existing folder.
func isDirAt(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// fixScopes is lore fix's folder scopes step (B3-5): each folder scope
// whose folder git saw renamed is relabelled with `lore scope move` (ids
// unchanged, no knowledge row rewritten); a gone folder git shows no
// rename for is listed with the command to run by hand, since only the
// person knows where it went. Skipped when the migrate step failed (the
// rows are not readable yet).
func (a *App) fixScopes(ctx context.Context, top string, dry, migrateFailed bool) opsdoctor.FixStep {
	st := opsdoctor.FixStep{Step: opsdoctor.StepScopes, Where: top, Result: opsdoctor.ResultCurrent}
	if migrateFailed {
		st.Result = "skipped: the migrate step failed"
		return st
	}
	gone, err := a.GoneScopes(ctx, top)
	if err != nil {
		if errs.Has(err, errs.NotFound) {
			return st // no lore project here: nothing scoped
		}
		st.Failed, st.Result = true, err.Error()
		return st
	}
	moved, manual := 0, 0
	for _, g := range gone {
		if g.RenamedTo == "" {
			manual++
			st.Detail = append(st.Detail, g.Line())
			continue
		}
		moved++
		cmd := "lore " + strings.Join(g.MoveArgs(), " ")
		if dry {
			st.Detail = append(st.Detail, "would run: "+cmd)
			continue
		}
		if _, merr := opsscope.Move(ctx, a, opsscope.MoveIn{Old: g.Path, New: g.RenamedTo}); merr != nil {
			st.Failed, st.Result = true, cmd+": "+merr.Error()
			return st
		}
		st.Detail = append(st.Detail, "ran: "+cmd)
	}
	switch {
	case moved > 0 && dry:
		st.Changed, st.Result = true, fmt.Sprintf("would relabel %d folder scope(s) git saw renamed", moved)
	case moved > 0:
		st.Changed, st.Result = true, fmt.Sprintf("relabelled %d folder scope(s) git saw renamed", moved)
	case manual > 0:
		st.Result = fmt.Sprintf("%d folder scope(s) name a folder that is gone; git shows no rename, so run lore scope move by hand", manual)
	}
	return st
}
