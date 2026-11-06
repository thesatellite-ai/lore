package host

// live_carry.go — F-MOVE-2b (coordinator decision 2026-11-06): a live row
// never names a row a teammate cannot reach. When a live write points (any
// ref field of its table definition) at a row that only this work tree
// holds, not landed on origin/<default>, that row rides the live ref with
// it as a companion, whatever run created it: `tasklist add` in one run,
// `task add --tasklist` (live) in the next. Same mechanism as the run's own
// new supporting rows (companionTables, FLAG6): marked Companion, so it
// never lands alone, and readers see it on the live ref (refTargets).
//
//	live row ─► ref fields (TableDef) ─► target in live? held this run? skip
//	          └► target in the work tree, not on origin/<default>,
//	             not in HEAD (a local unpushed commit keeps it: F-ADDADD-2)
//	             └► liveRows += target (Companion) ─► its own refs, same rule

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// neverLive are tables whose rows the live ref must never hold, so they
// are never carried: resources are branch only (RES RQ2).
var neverLive = map[row.Table]bool{schema.TableResources: true}

// tableDefs maps every built-in table to its definition.
func tableDefs() map[row.Table]schema.TableDef {
	defs := schema.Builtin()
	out := make(map[row.Table]schema.TableDef, len(defs))
	for _, d := range defs {
		out[d.Name] = d
	}
	return out
}

// refTargets is every table a live-readable table's ref fields point at:
// the tables a carried companion may belong to, which readers therefore
// also look up on the live ref. Computed once per session.
func (s *liveSession) refTargets() map[row.Table]bool {
	if s.targets != nil {
		return s.targets
	}
	s.targets = map[row.Table]bool{}
	for name, d := range tableDefs() {
		if !liveReadable(s, name) {
			continue
		}
		for _, f := range d.Fields {
			if f.RefTable != "" && !neverLive[f.RefTable] {
				s.targets[f.RefTable] = true
			}
		}
	}
	return s.targets
}

// readsLive: rows of t are looked up on the live ref (a live-readable
// table, or one a live row may carry as a companion).
func readsLive(s *liveSession, t row.Table) bool {
	return liveReadable(s, t) || s.refTargets()[t]
}

// refIDs is every (table, id) the ref fields of r name, by r's definition.
func refIDs(def schema.TableDef, r row.Row) []live.RowKey {
	var out []live.RowKey
	for _, f := range def.Fields {
		if f.RefTable == "" || neverLive[f.RefTable] {
			continue
		}
		v, ok := r.Fields[f.Name]
		if !ok || v == nil {
			continue
		}
		for _, t := range texts(v) {
			if id, err := ids.ParseAny(t); err == nil {
				out = append(out, live.RowKey{Table: string(f.RefTable), ID: id.String()})
			}
		}
	}
	return out
}

// texts is a ref value's ids as text: one for a single ref, each item of
// a list (a row read back from a file holds refs as text).
func texts(v value.Value) []string {
	if l, ok := v.(value.List); ok {
		out := make([]string, 0, len(l.Items()))
		for _, it := range l.Items() {
			out = append(out, it.String())
		}
		return out
	}
	if v.Kind() == value.KindNull || v.String() == "" {
		return nil
	}
	return []string{v.String()}
}

// carryUnlanded adds to liveRows, as companions, every row the rows of the
// write point at that only this work tree holds (not live, not held this
// run, not on origin/<default>), and the rows those point at in turn. It
// returns the carried keys: once the live write succeeds, the caller hands
// them to leaveWorkTree (F-ADDADD-1).
func (b *liveBackend) carryUnlanded(ctx context.Context, s *liveSession, files map[live.RowKey]live.File, liveRows map[live.RowKey]live.File, changes []store.Change, now time.Time) ([]live.RowKey, error) {
	defs := tableDefs()
	var carried []live.RowKey
	var queue []store.Row
	for _, ch := range changes {
		if _, ok := liveRows[keyOf(ch.Row)]; ok && !ch.Row.Deleted {
			queue = append(queue, ch.Row)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		def, ok := defs[cur.Table]
		if !ok {
			continue
		}
		r, err := cur.Decode()
		if err != nil {
			return nil, err
		}
		for _, k := range refIDs(def, r) {
			if _, in := liveRows[k]; in {
				continue
			}
			if _, in := files[k]; in {
				continue
			}
			if _, held := s.companions[k]; held {
				continue
			}
			id, err := ids.ParseAny(k.ID)
			if err != nil {
				continue
			}
			wt, err := b.inner.Get(ctx, row.Table(k.Table), id)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if wt.Deleted || b.landed(ctx, s, k) {
				continue
			}
			if b.committedHere(ctx, s, k) {
				s.warn(k.Table + " " + k.ID + " is committed here but not on " + s.ref.RemoteName + "/" + s.defaultBranch(ctx) + ": it stays on this branch, not live; teammates read it once that commit reaches " + s.defaultBranch(ctx))
				continue
			}
			f, err := liveFileFor(wt, nil, now, s)
			if err != nil {
				return nil, err
			}
			f.Block.Companion = true
			liveRows[k] = f
			carried = append(carried, k)
			queue = append(queue, wt)
		}
	}
	return carried, nil
}

// leaveWorkTree (F-ADDADD-1): a carried row MOVES to the live ref; its
// work-tree copy is removed, so the row lives in exactly one place. Kept,
// the copy went stale at the first edit (edits of a live row go live) and
// was committed with the user's next commit while the landing added the
// row's newer copy on origin/<default>: the next `git pull --rebase` hit
// an add/add conflict on the row file (docs tasks.sh link-commits). The
// row stays readable here from the live ref, then from origin/<default>
// once landed (read-through), then from the work tree after the pull.
//
// Only a copy HEAD does not hold leaves (untracked, or staged but never
// committed: the index entry goes too). A committed copy stays: removing
// it would commit a delete of a row the live ref still carries (and since
// F-ADDADD-2 such a row is never carried: committedHere).
// Best effort after the live write succeeded: a failure is one warning,
// never a failed write.
func (b *liveBackend) leaveWorkTree(ctx context.Context, s *liveSession, carried []live.RowKey) {
	for _, k := range carried {
		rel := mainDataPath(b.folder, k.Table+"/"+k.ID+rowFileExt)
		if _, committed, err := s.repo.ReadFile(ctx, "HEAD", rel); err != nil || committed {
			continue
		}
		if _, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"rm", "--cached", "-f", "-q", "--ignore-unmatch", "--", rel}}); err != nil {
			s.warn("carried row " + k.String() + ": could not unstage its work-tree copy: " + err.Error())
			continue
		}
		if err := os.Remove(filepath.Join(b.top, filepath.FromSlash(rel))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.warn("carried row " + k.String() + ": could not remove its work-tree copy: " + err.Error())
		}
	}
}

// committedHere (F-ADDADD-2) reports whether HEAD holds k's row file. A
// row not on origin/<default> that HEAD holds sits in a local commit not
// yet pushed (or not yet merged): it is NOT carried live. Carried, the
// row would live in two places: that commit adds the file, and the
// landing adds the live copy (newer after any edit, which goes live) on
// main, so the user's `git pull --rebase` (or the branch's merge) meets an
// add/add conflict on the row file, which the merge driver rightly refuses
// with no base. The other ways out were weighed and rejected: removing the
// file in a new commit does not help (the rebase replays the adding
// commit first), rewriting the user's commit is not ours to do, and
// teaching the landing or the merge driver that an add equal to an older
// live version is "already landed" changes semantics teammates rely on.
// Kept in git, the row travels with that commit and its edits stay on
// the branch (it is not a live row); the cost is that a teammate meets
// the id only once the commit reaches origin/<default>, which the warning
// says. An unreadable HEAD counts as not holding it (the carry as before).
func (b *liveBackend) committedHere(ctx context.Context, s *liveSession, k live.RowKey) bool {
	_, ok, err := s.repo.ReadFile(ctx, "HEAD", mainDataPath(b.folder, k.Table+"/"+k.ID+rowFileExt))
	return err == nil && ok
}

// landed reports whether k's row file is on the fetched origin/<default>
// (a teammate gets it with a pull). An unreadable ref counts as not
// landed: carrying a row that already landed is harmless, a missing one
// is the bug.
func (b *liveBackend) landed(ctx context.Context, s *liveSession, k live.RowKey) bool {
	remote := "refs/remotes/" + s.ref.RemoteName + "/" + s.defaultBranch(ctx)
	_, ok, err := s.repo.ReadFile(ctx, remote, mainDataPath(b.folder, k.Table+"/"+k.ID+rowFileExt))
	return err == nil && ok
}
