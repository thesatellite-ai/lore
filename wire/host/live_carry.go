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
//	          └► target in the work tree, not on origin/<default>
//	             └► liveRows += target (Companion) ─► its own refs, same rule

import (
	"context"
	"errors"
	"time"

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
// run, not on origin/<default>), and the rows those point at in turn.
func (b *liveBackend) carryUnlanded(ctx context.Context, s *liveSession, files map[live.RowKey]live.File, liveRows map[live.RowKey]live.File, changes []store.Change, now time.Time) error {
	defs := tableDefs()
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
			return err
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
				return err
			}
			if wt.Deleted || b.landed(ctx, s, k) {
				continue
			}
			f, err := liveFileFor(wt, nil, now, s)
			if err != nil {
				return err
			}
			f.Block.Companion = true
			liveRows[k] = f
			queue = append(queue, wt)
		}
	}
	return nil
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
