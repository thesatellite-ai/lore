package wire

// sync_store_conflicts.go — rows of an attached store that both this
// machine and the store's remote changed, so the store sync refused them
// (E_STALE / E_RESOURCE_CONFLICT; both edits are kept, the local commit
// waits). Before this, `lore sync conflicts` said "no conflicts", `lore
// explain` said the row was not found and nothing showed the other side's
// copy (bug hunt B2-2). Now:
//
//	lore sync conflicts ─▶ live (#1..L), marked row files (#L+1..F), then
//	                       these: store, fields both sides changed, mine
//	                       and theirs of each field (--json: both docs)
//	lore sync resolve n ─▶ git.Backend.SettleRow (kept = this machine's
//	                       copy, other = the remote's), then the store sync
//	                       that sends it (the same as `lore sync`)
//	lore explain <id>   ─▶ the store's copy, and the conflict when it has one
//
// The rows come from the refusal records `lore sync status` reads
// (storepush_refused.go); each is checked against the clone itself
// (RowSides), so a record a later edit already settled lists nothing.

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// rowSettler is a store backend that can show and settle a row both
// sides changed (the git backend in clone mode).
type rowSettler interface {
	RowSides(ctx context.Context, id ids.AnyID) (git.RowSides, bool, error)
	SettleRow(ctx context.Context, id ids.AnyID, takeTheirs bool) error
}

// StoreConflict is one row of an attached store both sides changed.
type StoreConflict struct {
	Store string
	Table row.Table
	ID    ids.AnyID
	// Fields are the fields both sides changed to different values
	// (merge.Rows' conflicts), each with this machine's and the remote's
	// value as text.
	Fields []StoreConflictField
	// MineDoc and TheirsDoc are the two row files.
	MineDoc, TheirsDoc []byte
}

// StoreConflictField is one field of a StoreConflict.
type StoreConflictField struct {
	Name, Mine, Theirs string
}

// storeConflictKept is the kept field of a store conflict: this machine's
// copy is the one every read here shows until it is settled.
const storeConflictKept = "mine"

// StoreConflicts lists the rows of attached stores that both sides
// changed and the store sync refused, by store then row id.
func (a *App) StoreConflicts(ctx context.Context) ([]StoreConflict, error) {
	recs, err := a.StorePushRefusals(ctx)
	if err != nil || len(recs) == 0 {
		return nil, err
	}
	a.attachHeldFolders(ctx, recs)
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	reg, err := builtinRegistry()
	if err != nil {
		return nil, err
	}
	var out []StoreConflict
	seen := map[string]bool{}
	for _, r := range recs {
		var rows []string
		switch {
		case r.HeldBy != "":
			// a push held by another folder's conflict: its rows, which
			// attachHeldFolders made readable when that folder is not
			// attached here (B2-27)
			for _, h := range recordHeldOn(r) {
				if settleable(h.Code) {
					rows = append(rows, h.Rows...)
				}
			}
		case settleable(r.Code):
			rows = r.Rows
		default:
			continue // a push the remote refused: not a row to settle
		}
		for _, raw := range rows {
			id, perr := ids.ParseAny(raw)
			if perr != nil || seen[raw] {
				continue
			}
			name, s, open, serr := st.rowSides(ctx, store.Source(r.Store), id)
			if serr != nil {
				return nil, serr
			}
			if !open {
				continue
			}
			seen[raw] = true
			c := StoreConflict{Store: string(name), Table: s.Table, ID: id, MineDoc: s.Mine, TheirsDoc: s.Theirs}
			c.Fields = conflictFields(reg, s)
			out = append(out, c)
		}
	}
	return out, nil
}

// settleable reports a refusal code of a row both sides changed.
func settleable(code string) bool {
	return errs.Code(code) == errs.Stale || errs.Code(code) == errs.ResourceConflict
}

// recordHeldOn is every folder whose refusal holds the record's push.
func recordHeldOn(r StorePushRefusal) []HeldFolder {
	if len(r.HeldOn) > 0 {
		return r.HeldOn
	}
	return []HeldFolder{{Folder: r.HeldBy, Code: r.Code, Rows: r.Rows}}
}

// folderNamer is a store backend that names its rowfs folder.
type folderNamer interface {
	Folder() string
}

// attachHeldFolders attaches, for this command only, each folder of the
// org's stores whose conflict holds a push here but which this repo does
// not attach (B2-27). A row written or edited through the by-id fallback
// (idfolder.go) lives in such a folder: its conflict held this repo's
// sync, yet `lore sync conflicts` and `resolve` saw nothing to settle.
func (a *App) attachHeldFolders(ctx context.Context, recs []StorePushRefusal) {
	want := map[string]bool{}
	for _, r := range recs {
		if r.HeldBy == "" {
			continue
		}
		for _, h := range recordHeldOn(r) {
			if settleable(h.Code) && h.Folder != "" {
				want[h.Folder] = true
			}
		}
	}
	if len(want) == 0 {
		return
	}
	st, err := a.state(ctx)
	if err != nil {
		return
	}
	for _, b := range st.byName {
		if f, ok := concrete(b).(folderNamer); ok {
			delete(want, f.Folder()) // attached here already
		}
	}
	if len(want) == 0 {
		return
	}
	for _, f := range a.orgFolders(ctx).Folders {
		if !want[f.Folder] {
			continue
		}
		delete(want, f.Folder)
		name := f.Org
		if name == "" {
			name = f.Label
		}
		a.attachExtra(attach.Store{Name: store.Source(name + targetSep + f.Folder), URL: f.URL, Folder: f.Folder, Branch: f.Branch})
	}
}

// rowSides is the conflict of id in the store the refusal record names
// or, where this command attached it under another name (inside the store
// folder itself, the name is the folder's), in any attached store that
// holds it; name is the store that answered.
func (st *state) rowSides(ctx context.Context, named store.Source, id ids.AnyID) (store.Source, git.RowSides, bool, error) {
	try := []store.Source{named}
	for n := range st.byName {
		if n != named {
			try = append(try, n)
		}
	}
	slices.Sort(try[1:])
	for _, n := range try {
		b, ok := concrete(st.byName[n]).(rowSettler)
		if !ok {
			continue
		}
		s, open, err := b.RowSides(ctx, id)
		if err != nil {
			return "", git.RowSides{}, false, err
		}
		if open {
			return n, s, true, nil
		}
	}
	return "", git.RowSides{}, false, nil
}

// conflictFields is the fields of s both sides changed differently, as
// the row merge sees them; a copy that does not decode lists nothing (the
// two docs still show it).
func conflictFields(reg *schema.Registry, s git.RowSides) []StoreConflictField {
	mine, err := row.Decode(s.Mine)
	if err != nil {
		return nil
	}
	theirs, err := row.Decode(s.Theirs)
	if err != nil {
		return nil
	}
	base := merge.Absent
	if len(s.Base) > 0 {
		if base, err = row.Decode(s.Base); err != nil {
			return nil
		}
	}
	p := merge.UnknownTable()
	if def, ok := reg.Table(mine.Table); ok {
		p = merge.PolicyFor(def)
	}
	res, err := merge.Rows(p, base, mine, theirs)
	if err != nil {
		return nil
	}
	out := make([]StoreConflictField, 0, len(res.Conflicts))
	for _, c := range res.Conflicts {
		out = append(out, StoreConflictField{Name: c.Field, Mine: conflictValue(mine, c.Field), Theirs: conflictValue(theirs, c.Field)})
	}
	return out
}

// maxConflictValue bounds a field value repeated in a conflict line.
const maxConflictValue = 200

// conflictValue is a field's value as one quoted line ("(none)" when absent).
func conflictValue(r row.Row, field string) string {
	v, ok := r.Fields[field]
	if !ok || v == nil {
		return "(none)"
	}
	t := v.String()
	if v.Kind() != value.KindText {
		if b, err := v.MarshalJSON(); err == nil {
			t = string(b)
		}
	}
	if len(t) > maxConflictValue {
		t = t[:maxConflictValue] + "…"
	}
	return strconv.Quote(t)
}

// storeConflictNumbered appends the store conflicts to out, numbered after
// what it already lists.
func storeConflictNumbered(out *opssync.ConflictsOut, cs []StoreConflict) {
	for _, c := range cs {
		n := len(out.Data) + 1
		names := make([]string, 0, len(c.Fields))
		for _, f := range c.Fields {
			names = append(names, f.Name)
		}
		what := "the row"
		if len(names) > 0 {
			what = strings.Join(names, ", ")
		}
		out.Data = append(out.Data, opssync.Conflict{ID: int64(n), Table: string(c.Table), RowID: c.ID.String(), Kept: storeConflictKept,
			OtherDoc: string(c.TheirsDoc), KeptDoc: string(c.MineDoc), Store: c.Store, Fields: names})
		out.Lines = append(out.Lines, fmt.Sprintf("#%d %s/%s kept=%s open (store %s: both sides changed %s; settle with lore sync resolve --id %d --take kept|other, or edit the row so both agree)",
			n, c.Table, c.ID, storeConflictKept, c.Store, what, n))
		for _, f := range c.Fields {
			out.Lines = append(out.Lines, "    "+f.Name+" mine (kept):    "+f.Mine, "    "+f.Name+" theirs (other): "+f.Theirs)
		}
	}
	out.Count = len(out.Data)
}

// ResolveStoreConflict settles c (takeTheirs: the remote's copy, else this
// machine's) and runs the store sync that sends it.
func (a *App) ResolveStoreConflict(ctx context.Context, c StoreConflict, takeTheirs bool) error {
	st, err := a.state(ctx)
	if err != nil {
		return err
	}
	b, ok := concrete(st.byName[store.Source(c.Store)]).(rowSettler)
	if !ok {
		return errs.New(errs.Unsupported, "resolve: store "+c.Store+" cannot settle a row here")
	}
	if err := b.SettleRow(ctx, c.ID, takeTheirs); err != nil {
		return err
	}
	if _, err := a.SyncStores(ctx, ""); err != nil {
		return errs.Prefix("resolve: settled "+c.ID.String()+", but the sync", err)
	}
	return nil
}

// explainStore adds the attached store's copy of id to a host answer that
// found it nowhere in this repo: the store, which copy wins, and the
// conflict when both sides changed it.
func (a *App) explainStore(ctx context.Context, out opssync.ExplainOut, id string) (opssync.ExplainOut, bool) {
	aid, err := ids.ParseAny(id)
	if err != nil {
		return out, false
	}
	src, r, err := a.Get(ctx, engine.Ref{ID: aid})
	if err != nil || src == "" || src == store.SourceRepo {
		return out, false
	}
	lines := slices.DeleteFunc(slices.Clone(out.Lines), func(l string) bool { return out.Reason != "" && l == "  "+out.Reason })
	if out.ID == "" {
		out.ID = id
		lines = []string{id}
	}
	state := "row"
	if r.Deleted {
		state = "tombstone"
	}
	lines = append(lines, fmt.Sprintf("  store %s: %s (%s), updated %s", src, state, r.Table, r.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")))
	out.Copies = append(out.Copies, opssync.ExplainCopy{Place: opssync.ExplainStore, Store: string(src), Present: true, State: state, HLC: r.HLC.String(), Winner: true})
	out.Winner = opssync.ExplainStore
	out.Reason = "the copy in store " + string(src) + " (the row lives in that store, not in this repo)"
	lines = append(lines, "  wins: "+out.Reason)
	if cs, cerr := a.StoreConflicts(ctx); cerr == nil {
		for _, c := range cs {
			if c.ID != aid {
				continue
			}
			names := make([]string, 0, len(c.Fields))
			for _, f := range c.Fields {
				names = append(names, f.Name)
			}
			lines = append(lines, "  CONFLICT: the store's remote holds another edit ("+strings.Join(names, ", ")+"); both are kept and this machine's waits to be pushed",
				"  see both: lore sync conflicts; settle: lore sync resolve --id <n> --take kept|other, or edit the row so both agree")
			for i := range out.Copies {
				if out.Copies[i].Place == opssync.ExplainStore {
					out.Copies[i].Conflict = true
				}
			}
		}
	}
	out.Lines = lines
	return out, true
}
