package wire

// history.go — the git versions of one row for `task history --git`,
// `meta history` and `resource history --git` (OV62; TASK 17.2,
// METADATA 3.19, RESOURCES 3.22).
//
//	ops ─ upgrade.As[opstask.RowHistorian](env) ─ (*App).RowVersions
//	  ├─ ref.Store "" → engine.Get finds the store holding the row
//	  ├─ concrete(byName[store]).(store.RowHistory): branch versions
//	  │    absent (folder backend) → E_UNSUPPORTED with a hint
//	  ├─ repo store: + versions on refs/lore/live and its tracking copy
//	  │    (open rows of live kinds are committed there, never on the
//	  │    branch, until they land; LORE_TEAM_SPEC F1)
//	  └─ order by the row's HLC (a person may commit a landed copy long
//	     after the edit, so commit order is not edit order), one version
//	     per HLC; At = the row's updated_at, By = its updated_by (the git
//	     author alone is ambiguous when one machine writes for many
//	     actors, METADATA 3.19), else the commit's

import (
	"bytes"
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/store"
)

var _ opstask.RowHistorian = (*App)(nil)

// RowVersions implements opstask.RowHistorian: the committed versions of
// ref's row in the store that holds it, oldest first.
func (a *App) RowVersions(ctx context.Context, ref engine.Ref) ([]opstask.RowVersion, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	src := ref.Store
	if src == "" || ref.Table == "" {
		var r row.Row
		if src, r, err = st.eng.Get(ctx, ref); err != nil {
			return nil, err
		}
		ref.Table = r.Table
	}
	b, ok := st.byName[src] // fixed at open; read without App.mu like the other readers
	if !ok {
		return nil, errs.New(errs.NotFound, "wire: store "+string(src)+" is not attached")
	}
	h, ok := concrete(b).(store.RowHistory)
	if !ok {
		return nil, errs.New(errs.Unsupported, "store "+string(src)+" keeps no history of its rows (a plain folder store)").
			WithHint("history of old versions needs a git store; the row's own history list still shows recent changes")
	}
	vers, err := h.Versions(ctx, ref.Table, ref.ID)
	if err != nil {
		return nil, err
	}
	out := make([]opstask.RowVersion, 0, len(vers))
	for _, v := range vers {
		r, derr := v.Row.Decode()
		if derr != nil {
			continue // Versions already decoded it once; never guess a version
		}
		out = append(out, opstask.RowVersion{Commit: v.Commit, At: v.At, By: v.Author, Row: r})
	}
	if src == store.SourceRepo && st.repoTop != "" {
		for _, lref := range []string{liveRefLocal, liveRefTracking} {
			lv, lerr := liveVersions(ctx, st.repoTop, lref, ref.Table, ref.ID)
			if lerr != nil {
				return nil, lerr
			}
			out = append(out, lv...)
		}
	}
	return orderVersions(out), nil
}

// orderVersions sorts by the row's HLC (commit time breaks a tie), keeps
// one version per HLC, and stamps At/By from the row's audit fields.
func orderVersions(vs []opstask.RowVersion) []opstask.RowVersion {
	slices.SortStableFunc(vs, func(x, y opstask.RowVersion) int {
		return cmp.Or(x.Row.HLC.Compare(y.Row.HLC), x.At.Compare(y.At))
	})
	out := make([]opstask.RowVersion, 0, len(vs))
	for _, v := range vs {
		if n := len(out); n > 0 && v.Row.HLC.Compare(out[n-1].Row.HLC) == 0 {
			continue // the same edit seen on the branch and the live ref
		}
		if !v.Row.UpdatedAt.IsZero() {
			v.At = v.Row.UpdatedAt
		}
		if !v.Row.UpdatedBy.IsZero() {
			v.By = v.Row.UpdatedBy.String()
		}
		out = append(out, v)
	}
	return out
}

// liveLogSep separates the fields of one live log header line.
const liveLogSep = "\x1f"

// liveLogMark starts a header line (the path lines --name-only adds follow).
const liveLogMark = "\x1e"

// liveLogFields is commit, author date, author name.
const liveLogFields = 3

// liveVersions is the versions of one row on a live ref ("" ref absent →
// none). The live tree files rows under a project folder (`_root` or the
// project's path), so the path is matched by glob and read per commit.
func liveVersions(ctx context.Context, top, ref string, table row.Table, id ids.AnyID) ([]opstask.RowVersion, error) {
	if !liveRefExists(ctx, top, ref) {
		return nil, nil
	}
	spec := ":(glob)**/" + table.String() + "/" + id.String() + liveRowExt
	log, err := liveGit(ctx, top, "log", "--no-renames", "--name-only",
		"--format="+liveLogMark+"%H"+liveLogSep+"%aI"+liveLogSep+"%an", ref, "--", spec)
	if err != nil {
		return nil, err
	}
	var out []opstask.RowVersion
	for block := range strings.SplitSeq(string(log), liveLogMark) {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		head := strings.Split(lines[0], liveLogSep)
		if len(head) != liveLogFields || len(lines) < 2 {
			continue // a commit that deleted the file lists it too; read fails below and is skipped
		}
		at, perr := time.Parse(time.RFC3339, head[1])
		if perr != nil {
			continue
		}
		path := strings.TrimSpace(lines[len(lines)-1])
		doc, gerr := liveGit(ctx, top, "cat-file", "blob", head[0]+":"+path)
		if gerr != nil {
			continue // the commit removed the file (landed or deleted)
		}
		r, derr := decodeLiveRow(bytes.TrimSpace(doc))
		if derr != nil || r.Table != table || r.ID != id {
			continue // never guess a version
		}
		out = append(out, opstask.RowVersion{Commit: head[0], At: at.UTC(), By: head[2], Row: r})
	}
	return out, nil
}
