package engine

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/metafilter"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/timecalc"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Keys of one move entry in _o (PROVENANCE_DESIGN 5.4: {at, by,
// moved_from, moved_to}). The entries are kept in the `moves` list of _o
// because row.Row has no bookkeeping field of its own (spec-deltas.md);
// moved_from / moved_to are each attachment's Place (sto_ id, plus the
// folder's prj_ id; FQ4), or its attached name where no Place is known.
const (
	originMoves  = "moves"
	moveAt       = "at"
	moveBy       = "by"
	moveFrom     = "moved_from"
	moveTo       = "moved_to"
	listOpen     = "["
	listClose    = "]"
	listSep      = ","
	emptyJSONObj = "{}"
)

// Move moves one live row to the attached store to, keeping its id (PROV5):
// the row is written to to (validated, ref-checked and secret-scanned under
// to's policy, like a create there) with a `moves` entry appended to its _o,
// then a tombstone is written where it was. ref.Store, when set, names the
// source; otherwise every attached store except to is searched.
//
// Refusals (nothing written): to not attached or read-only; the row is
// already in to; to holds a tombstone of it (rows never come back to a
// place they left, spec 11 rule 3); to is a client repo and the row's text
// holds secret references (BATCH 5, the references are listed).
//
// Interrupted after the first write, the row is in both stores; running
// Move again finds the copy in to and only writes the source tombstone.
func (e *Engine) Move(ctx context.Context, ref Ref, to store.Source) (Written, error) {
	return e.MoveWith(ctx, ref, to, MoveOptions{})
}

// TimeShare is `lore move --with-time` (TQ3, CLI_REFERENCE 2.7): what a
// task moved into a client repo carries of its time.
type TimeShare string

// The --with-time values. TimeNone is the default (client repos get no
// time data, TQ3).
const (
	TimeNone   TimeShare = "none"
	TimeTotals TimeShare = "totals"
)

// TimeShares lists the --with-time values.
func TimeShares() []string { return []string{string(TimeNone), string(TimeTotals)} }

// MoveOptions are the choices of one move beyond the row and the target.
type MoveOptions struct {
	// DropInternal accepts losing the row's internal metadata values on a
	// move into a client repo (MQ11, BATCH 7); without it such a move is
	// refused with E_META_INTERNAL. The dropped keys come back as warnings.
	DropInternal bool
	// WithTime is --with-time (TQ3): "" follows the target's
	// time.client_share policy (default none), TimeNone carries no time for
	// this move, TimeTotals writes the time_totals projection.
	WithTime TimeShare
	// WithChildren is --with-children (TASK 6.14, B7): a task moves with
	// its whole subtree; every child keeps its id and parent_id. Without
	// it a task moves alone (its children stay where they are).
	WithChildren bool
}

// WarnMetaDropped names a metadata value --drop-internal removed (MQ11).
const WarnMetaDropped WarnCode = "W_META_DROPPED"

// MoveWith is Move with options (--drop-internal, --with-time).
//
//	locate the one live copy ─► refuse (target tombstone, secret refs,
//	internal meta into a client repo: E_META_INTERNAL unless DropInternal)
//	─► write at to (+ moves entry, or the client _o) ─► tombstone at src
//	─► WithChildren: each sub-task the same way, parents first (B7)
func (e *Engine) MoveWith(ctx context.Context, ref Ref, to store.Source, opt MoveOptions) (Written, error) {
	switch opt.WithTime {
	case "", TimeNone, TimeTotals:
	default:
		return Written{}, errs.New(errs.InvalidInput, "--with-time takes totals or none, not "+strconv.Quote(string(opt.WithTime)))
	}
	if err := e.refuseMachineMove(ctx, ref, to); err != nil { // P3-LOCAL-1
		return Written{}, err
	}
	cl := e.callOf(ctx)
	dst, err := e.writable(ctx, to)
	if err != nil {
		return Written{}, err
	}
	src, cur, err := e.locateExcept(ctx, ref, to)
	if err != nil {
		return Written{}, err
	}
	srcSt, err := e.writable(ctx, src)
	if err != nil {
		return Written{}, err
	}
	if cur.Deleted {
		// A move run again after it finished finds only the old copy's
		// tombstone outside to: say the row is already there instead of
		// "is deleted" (which reads as data loss). The code stays E_NOT_FOUND:
		// there is nothing to move from elsewhere (engine TestMove).
		if there, _, gerr := e.getRow(ctx, to, cur.Table, cur.ID); gerr == nil && !there.Deleted {
			return Written{}, errs.Wrap(errs.NotFound, ""+cur.ID.String()+" is already in store "+string(to)+"; nothing to move", store.ErrNotFound).
				WithHint("lore row show --id " + cur.ID.String() + " --origin shows where it lives")
		}
		return Written{}, deletedErr(ctx, srcSt, cur, "is deleted")
	}
	// METADATA 3.13 / 6 (X19): a row whose metadata is quarantined never
	// travels; it is fixed first with the commands lore meta show prints
	if probs := metafilter.Quarantine(cur.Meta); len(probs) > 0 {
		return Written{}, errs.New(errs.MetaQuarantined, ""+cur.ID.String()+" has quarantined metadata ("+probs[0].Text+"); it is not moved until it is fixed").
			WithHint("lore meta show --id " + cur.ID.String() + " prints each problem and its fix")
	}
	def, err := e.tableDef(cur.Table)
	if err != nil {
		return Written{}, err
	}
	// TASK 6.14: a sub-task never reaches a client repo without its parent.
	if def.Name == schema.TableTasks && dst.Client {
		if err = e.refuseOrphanChild(ctx, to, cur); err != nil {
			return Written{}, err
		}
	}
	var kids []ids.AnyID
	if opt.WithChildren {
		if def.Name != schema.TableTasks {
			return Written{}, errs.New(errs.InvalidInput, "--with-children moves a task's sub-tasks; "+cur.ID.String()+" is not a task")
		}
		if kids, err = e.taskDescendants(ctx, to, cur.ID); err != nil { // read before the parent leaves src
			return Written{}, err
		}
	}
	actor := e.actorOf(cl)
	there, _, err := e.getRow(ctx, to, cur.Table, cur.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		there = store.Row{}
	case err != nil:
		return Written{}, err
	case there.Deleted:
		if perr := refusePurged(ctx, dst, cur.Table, cur.ID, false); perr != nil {
			return Written{}, perr // purged there: E_PURGED names who and when
		}
		return Written{}, errs.New(errs.Unsupported, ""+cur.ID.String()+" was in store "+string(to)+" before and left it; a row never returns to a place it left").
			WithHint("keep it in " + string(src))
	}
	var moved row.Row
	var dropped []Warning
	fresh := there.ID.IsZero() // false on a rerun that only removes the old copy
	if fresh {
		// A row read back from a backend carries inferred values (a ref is
		// Text, a json array of objects a List of JSON items), and
		// validate.Targets only sees typed refs: re-type it first, or the
		// reference, F15 and secret-ref checks miss what the file form
		// hides. Not a whole-row validation: a row the source accepted (a
		// v0.1.17 row with extra columns) still moves.
		typed := retype(def, cur.Fields)
		// F-MOVE-1: a container only the source holds is left behind,
		// never carried as a dangling id the next hop cannot resolve.
		var left []Warning
		if typed, left, err = e.leaveContainers(ctx, src, to, def, typed); err != nil {
			return Written{}, err
		}
		if dst.Client {
			if err = refuseSecretRefs(row.Row{ID: cur.ID, Fields: typed}); err != nil {
				return Written{}, err
			}
			// RES 3.9: a resource's foreign refs, internal and sensitive values.
			var resDropped []Warning
			if def.Name == schema.TableResources {
				if typed, resDropped, err = e.resourceIntoClient(ctx, to, cur.ID, typed, opt.DropInternal); err != nil {
					return Written{}, err
				}
			}
			if typed, err = e.rehomeProject(ctx, to, def, typed, true); err != nil {
				return Written{}, err
			}
			if cur.Meta, dropped, err = e.internalMeta(ctx, cur, opt.DropInternal); err != nil {
				return Written{}, err
			}
			// TQ3: internal fields (time_entries, time_open, claim state)
			// never reach a client; time may travel as totals only.
			var kept []Warning
			if typed, kept, err = e.clientTime(ctx, def, to, typed, opt.WithTime); err != nil {
				return Written{}, err
			}
			dropped = append(append(dropped, kept...), resDropped...)
			// BATCH 3: the client repo's own kind row (origin) and field
			// rows, written last so a refusal above leaves nothing behind.
			if def.Name == schema.TableResources {
				if typed, err = e.clientKind(ctx, to, typed); err != nil {
					return Written{}, err
				}
			}
		} else {
			// F-MOVE-1: the row belongs to the place it moved to when that
			// place has one project; otherwise its project stays as it was.
			if typed, err = e.rehomeProject(ctx, to, def, typed, false); err != nil {
				return Written{}, err
			}
			if n, secs := movedTime(typed); n > 0 {
				// TIME 11.5 (FA-LTASK-4): the entries travel with the row; the
				// line tells the old project its history went too.
				dropped = append(dropped, Warning{Code: WarnTimeMoved, Field: fieldTimeEntries,
					Message: strconv.Itoa(n) + " time entries (" + timeentry.FormatSeconds(secs) + ") moved with the task"})
			}
		}
		dropped = append(dropped, left...)
		names := slices.Sorted(maps.Keys(typed))
		if !dst.Client { // F-MOVE-1: a kept parent_id nobody here holds is carried (TASK 6.8)
			if names, err = e.carriedEdges(ctx, def, typed, names); err != nil {
				return Written{}, err
			}
		}
		if err = e.checkRefs(ctx, to, def, typed, names); err != nil {
			return Written{}, err
		}
		var sc screened
		if sc, err = e.screen(ctx, cl, to, cur.ID, typed, names); err != nil {
			return Written{}, err
		}
		// RQ2: resources.allowed_in of the target applies to a move too.
		if err = enforceMovePlace(sc.pol, def, to, dst.Client); err != nil {
			return Written{}, err
		}
		moved = cur
		moved.Fields = sc.fields
		moved.HLC = e.clock.Now(cur.HLC)
		moved.UpdatedAt, moved.UpdatedBy = moved.HLC.Wall, actor
		if dst.Client {
			// A client repo never learns where the row came from: the
			// row keeps only the client-safe part of its own _o and no
			// moves entries (PROVENANCE_DESIGN 8.1, PROV7, PROV8).
			if moved.Origin, err = e.movedIntoClient(ctx, dst, cur.Origin); err != nil {
				return Written{}, err
			}
		} else if moved.Origin, err = withMove(cur.Origin, moved.HLC, actor.String(), e.placeOf(src), e.placeOf(to)); err != nil {
			return Written{}, err
		}
		var got stored
		if got, err = e.put(ctx, dst, moved, hlc.HLC{}); err != nil {
			return Written{}, err
		}
		ev := Event{Op: cl.op, Action: ActionCreate, Store: to, Table: cur.Table, ID: cur.ID, HLC: moved.HLC, Actor: actor,
			SecretsAccepted: sc.accepted, SecretsRedacted: sc.redacted}
		var aw []Warning
		if aw, err = e.finishW(ctx, to, []store.Row{got.doc}, ev); err != nil {
			return Written{}, err
		}
		dropped = append(dropped, aw...) // W_AUDIT_DEFERRED of the copy (later 542)
	} else if moved, err = there.Decode(); err != nil {
		return Written{}, err
	}
	// batch leftover-3: in a stage (wire withActorRows), the rows staged
	// for to (the caller's first actor row there, the copy, a client
	// repo's kind rows) are written now as one batch, before the old copy
	// is removed: a refusal above discarded them with the stage, so a
	// refused move leaves no actor row in to; and the copy is in to before
	// its source tombstone is staged (a stage writes one store).
	if pre := absorbStaged(ctx, to); len(pre.changes) > 0 {
		bc, bev := pre.withDirect(nil, nil)
		if err = e.writeChanges(ctx, dst, bc, bev); err != nil {
			return Written{}, err
		}
		fd, fev := pre.finishDirect(nil, nil)
		aw, ferr := e.finishW(unstaged(ctx), to, fd, fev...)
		dropped = append(dropped, aw...)
		if ferr != nil {
			return Written{Row: moved, Warnings: dropped}, ferr
		}
	}
	h := e.clock.Now(cur.HLC)
	tomb := row.Row{Table: cur.Table, ID: cur.ID, HLC: h, Deleted: true, UpdatedAt: h.Wall, UpdatedBy: actor}
	// PROVENANCE 5.4: the source place's commit records where the row went
	// (Lore-Moved-To: the target's place id; for a client repo its rep_
	// id), so the history our side keeps can be stitched across a hop the
	// client file never names. Per write: only this tombstone's commit.
	// Written directly even in a stage (its own commit carries the
	// trailer; a stage writes one store and the copy is in to already).
	mctx := store.WithCommitTrailers(unstaged(ctx), MovedToTrailer+": "+e.placeOf(to))
	got, err := e.put(mctx, srcSt, tomb, cur.HLC)
	if err != nil {
		return Written{Row: moved}, errs.Prefix(""+cur.ID.String()+" is now in "+string(to)+" but its old copy in "+string(src)+" could not be removed", err).
			WithHint("run the same move again: it only removes the old copy")
	}
	ev := Event{Op: cl.op, Action: ActionDelete, Store: src, Table: cur.Table, ID: cur.ID, HLC: h, Actor: actor}
	w := Written{Row: moved, Warnings: dropped}
	aw, err := e.finishW(unstaged(ctx), src, []store.Row{got.doc}, ev)
	w.Warnings = append(w.Warnings, aw...)
	if err != nil {
		return w, err
	}
	if fresh && def.Name == schema.TableTasks {
		if err = e.noteMovedAlone(ctx, to, moved, &w); err != nil { // TASK 6.8 (OVERNIGHT Qs)
			return w, err
		}
	}
	return w, e.moveChildren(ctx, to, kids, opt, &w) // B7: parents first, ids kept
}

// internalMeta returns r's metadata without its internal keys' values, for
// a move into a client repo (MQ11: internal keys never reach a client).
// Without drop a row holding any is refused with E_META_INTERNAL naming
// them (BATCH 7: the row keeps no other copy, so losing them is a choice).
func (e *Engine) internalMeta(ctx context.Context, r row.Row, drop bool) (map[ids.ID[ids.MetaKey]]value.JSON, []Warning, error) {
	if len(r.Meta) == 0 {
		return r.Meta, nil, nil
	}
	reg, err := e.metaRegistry(ctx)
	if err != nil {
		return nil, nil, err
	}
	internal := map[ids.ID[ids.MetaKey]]string{}
	for _, d := range reg {
		if d.internal {
			internal[d.id] = d.name
		}
	}
	var names []string
	out := make(map[ids.ID[ids.MetaKey]]value.JSON, len(r.Meta))
	for k, v := range r.Meta {
		if n, ok := internal[k]; ok {
			names = append(names, n)
			continue
		}
		out[k] = v
	}
	if len(names) == 0 {
		return r.Meta, nil, nil
	}
	slices.Sort(names)
	if !drop {
		return nil, nil, errs.New(errs.MetaInternal, ""+r.ID.String()+" holds internal metadata ("+strings.Join(names, ", ")+") that must not reach a client repo; nothing written").
			WithHint("lore move --id " + r.ID.String() + " --to <target> --drop-internal (removes them), or keep the row in the store")
	}
	ws := make([]Warning, 0, len(names))
	for _, n := range names {
		ws = append(ws, Warning{Code: WarnMetaDropped, Field: n, Message: "kept back: internal metadata " + n + " was removed (--drop-internal)"})
	}
	return out, ws, nil
}

// locateExcept finds ref's row in ref.Store, or in every attached store
// but skip (exactly one may hold it).
func (e *Engine) locateExcept(ctx context.Context, ref Ref, skip store.Source) (store.Source, row.Row, error) {
	if ref.Store != "" {
		return e.locate(ctx, ref)
	}
	var found []copyHit
	seen := map[string]bool{} // Locations already looked in (see Attached.Location)
	if loc := e.stores[skip].Location; loc != "" {
		seen[loc] = true // another name of the skipped place is the same place
	}
	for _, name := range e.order {
		if name == skip {
			continue
		}
		if loc := e.stores[name].Location; loc != "" {
			if seen[loc] {
				continue
			}
			seen[loc] = true
		}
		src, r, err := e.locate(ctx, Ref{Store: name, Table: ref.Table, ID: ref.ID})
		if errs.Has(err, errs.NotFound) {
			continue
		}
		if err != nil {
			return "", row.Row{}, err
		}
		found = append(found, copyHit{src: src, row: r})
	}
	if len(found) == 0 {
		return "", row.Row{}, errs.Wrap(errs.NotFound, "no row "+ref.ID.String()+" outside "+string(skip), store.ErrNotFound)
	}
	h, err := pickCopy(ref.ID, found) // a live copy wins over tombstones
	if err != nil {
		return "", row.Row{}, err
	}
	return h.src, h.row, nil
}

// refuseSecretRefs refuses a row whose text holds ${secret:…} references
// (BATCH 5: a client repo must never receive them), listing each.
func refuseSecretRefs(r row.Row) error {
	var found []string
	for _, name := range slices.Sorted(maps.Keys(r.Fields)) {
		for _, t := range texts(r.Fields[name]) {
			for _, ref := range refs.References(t) {
				if ref.Scheme == refs.SchemeSecret {
					found = append(found, name+": "+ref.String())
				}
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	// FLAG5 / SQ17 / BATCH 5: a secret reference names our vault and its
	// holder, which a client must never read: the leak guard's code.
	return errs.New(errs.LeakBlocked, ""+r.ID.String()+" holds secret references and cannot live in a client repo: "+strings.Join(found, ", ")+"; nothing written").
		WithHint("remove the references from the row first, or keep the row in one of your own stores (BATCH 5)")
}

// texts returns the text of a value in any form it is held in: a text, a
// json value's raw text, or the texts of every item of a list (a list read
// back from a file may hold json objects as items: value.Infer).
func texts(v value.Value) []string {
	switch x := v.(type) {
	case value.Text:
		return []string{x.String()}
	case value.JSON:
		return []string{x.String()}
	case value.List:
		var out []string
		for _, it := range x.Items() {
			out = append(out, texts(it)...)
		}
		return out
	}
	return nil
}

// withMove returns origin with one more entry in its `moves` list.
func withMove(origin value.JSON, at hlc.HLC, by, from, to string) (value.JSON, error) {
	raw := []byte(emptyJSONObj)
	if !origin.IsZero() {
		raw = origin.Raw()
	}
	ms, err := canon.Members(raw)
	if err != nil {
		return value.JSON{}, errs.Wrap(errs.Internal, "read _o", err)
	}
	entry, err := canon.Object([]canon.Member{
		{Key: moveAt, Raw: canon.String(value.DateTimeFrom(at.Wall).String())},
		{Key: moveBy, Raw: canon.String(by)},
		{Key: moveFrom, Raw: canon.String(from)},
		{Key: moveTo, Raw: canon.String(to)},
	})
	if err != nil {
		return value.JSON{}, errs.Wrap(errs.Internal, "build a move entry", err)
	}
	var list [][]byte
	keep := ms[:0]
	for _, m := range ms {
		if m.Key == originMoves {
			if list, err = canon.Elements(m.Raw); err != nil {
				return value.JSON{}, errs.Wrap(errs.Internal, "_o.moves is not a list", err)
			}
			continue
		}
		keep = append(keep, m)
	}
	list = append(list, entry)
	parts := make([]string, len(list))
	for i, b := range list {
		parts[i] = string(b)
	}
	keep = append(keep, canon.Member{Key: originMoves, Raw: []byte(listOpen + strings.Join(parts, listSep) + listClose)})
	obj, err := canon.Object(keep)
	if err != nil {
		return value.JSON{}, errs.Wrap(errs.Internal, "build _o", err)
	}
	return value.JSONOf(obj)
}

// projectField is the field naming a row's project (every project-scoped
// table: schema.Builtin).
const projectField = "project_id"

// rehomeProject points a row moving into to at that store's project. For
// a client repo (strict) the source's project is an internal store
// folder's, whose id must not reach the client (and F15 refuses it), so to
// must hold exactly one live project, else the move is refused (nothing
// written). For our own places (not strict, F-MOVE-1) the row takes to's
// project when to holds exactly one and keeps its own otherwise. A row
// whose project already lives in to is unchanged either way.
func (e *Engine) rehomeProject(ctx context.Context, to store.Source, def schema.TableDef, fields map[string]value.Value, strict bool) (map[string]value.Value, error) {
	f, ok := def.Field(projectField)
	cur, has := fields[projectField]
	if !ok || !has || f.RefTable != schema.TableProjects {
		return fields, nil
	}
	if id, err := ids.ParseAny(cur.String()); err == nil {
		if got, _, gerr := e.getRow(ctx, to, schema.TableProjects, id); gerr == nil && !got.Deleted {
			return fields, nil
		}
	}
	projects, err := e.List(ctx, to, schema.TableProjects)
	if err != nil {
		return nil, err
	}
	if len(projects) != 1 {
		if !strict {
			return fields, nil
		}
		return nil, errs.New(errs.Unsupported, "store "+string(to)+" holds "+strconv.Itoa(len(projects))+" projects; a row moving into it needs exactly one to belong to").
			WithHint("run `lore init` in the client repo first")
	}
	ref, err := value.RefOf(projects[0].ID)
	if err != nil {
		return nil, err
	}
	out := maps.Clone(fields)
	out[projectField] = ref
	return out, nil
}

// MovedToTrailer is the commit trailer key a move adds to the source
// place's commit (PROVENANCE 5.4).
const MovedToTrailer = "Lore-Moved-To"

// placeOf is the place id a `moves` entry records for the attached store
// name (FQ4): its Place, or the attached name when wire knows no place id
// (the repo store, test backends). The merge unions both forms alike.
func (e *Engine) placeOf(name store.Source) string {
	if p := e.stores[name].Place; p != "" {
		return p
	}
	return string(name)
}

// Time fields of a task row the client projection reads and writes
// (schema tasks; TIME_TRACKING_DESIGN 8).
const (
	fieldTimeEntries = "time_entries"
	fieldTimeOpen    = "time_open"
	fieldTimeTotals  = "time_totals"
	fieldDisplayName = "display_name"
	fieldActorKind   = "kind"
	// unknownWorker labels a worker whose actor row is not readable: the
	// actor id itself must not reach the client.
	unknownWorker = "unknown"
)

// Warnings of a move into a client repo (TQ3).
const (
	// WarnTimeKeptBack: the task's time entries stayed behind.
	WarnTimeKeptBack WarnCode = "W_TIME_KEPT_BACK"
	// WarnInternalKeptBack: other internal fields stayed behind.
	WarnInternalKeptBack WarnCode = "W_INTERNAL_KEPT_BACK"
	// WarnTimeTotals: the move carried totals only.
	WarnTimeTotals WarnCode = "W_TIME_TOTALS"
	// WarnTimeMoved: a move between our own places carried the task's time
	// entries with it (TIME 11.5); a notice, nothing was dropped.
	WarnTimeMoved WarnCode = "W_TIME_MOVED"
)

// movedTime counts the live closed time entries of a row's fields and
// their seconds (TIME 11.5).
func movedTime(fields map[string]value.Value) (int, int64) {
	entries, _ := decodeTime(fields)
	var secs int64
	for _, en := range entries {
		secs += en.Seconds
	}
	return len(entries), secs
}

// TimeTotalsDoc is the time_totals object (design 8 point 4): per worker
// label and kind (display name only, the client projection), seconds, and
// the date range; no spans, no sessions, no notes.
type TimeTotalsDoc struct {
	From          string        `json:"from,omitempty"`
	To            string        `json:"to,omitempty"`
	Workers       []TotalWorker `json:"workers"`
	EffortSeconds int64         `json:"effort_seconds"`
}

// TotalWorker is one worker line of TimeTotalsDoc.
type TotalWorker struct {
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Seconds int64  `json:"seconds"`
}

// clientTime strips def's internal fields from a row moving into the
// client repo to (FieldDef.Internal: never written into a client repo) and,
// when the share is totals, writes time_totals (TQ3):
//
//	share = --with-time, else to's time.client_share (default none)
//	internal fields → dropped, one warning each ("kept back: time (12 entries)")
//	share totals and any time → time_totals from timecalc per actor
func (e *Engine) clientTime(ctx context.Context, def schema.TableDef, to store.Source, fields map[string]value.Value, opt TimeShare) (map[string]value.Value, []Warning, error) {
	share := opt
	fromPolicy := false
	if share == "" {
		pol, err := e.Policy(ctx, to)
		if err != nil {
			return nil, nil, err
		}
		share = TimeNone
		if ef, set := pol.Get(policy.KeyTimeClientShare); set && ef.Value != nil && ef.Value.String() == string(TimeTotals) {
			share, fromPolicy = TimeTotals, true
		}
	}
	entries, open := decodeTime(fields)
	out := maps.Clone(fields)
	var warns []Warning
	var other []string
	for _, f := range def.Fields {
		if !f.Internal {
			continue
		}
		if _, ok := out[f.Name]; !ok {
			continue
		}
		delete(out, f.Name)
		if f.Name != fieldTimeEntries && f.Name != fieldTimeOpen {
			other = append(other, f.Name)
		}
	}
	// a stale time_totals of an earlier move never survives a new one
	delete(out, fieldTimeTotals)
	if n := len(entries) + len(open); n > 0 {
		warns = append(warns, Warning{Code: WarnTimeKeptBack, Field: fieldTimeEntries, Message: "kept back: time (" + strconv.Itoa(n) + " entries)"})
	}
	if len(other) > 0 {
		warns = append(warns, Warning{Code: WarnInternalKeptBack, Message: "kept back: internal fields (" + strings.Join(other, ", ") + ")"})
	}
	if share != TimeTotals || len(entries)+len(open) == 0 {
		return out, warns, nil
	}
	tt := e.timeTotals(ctx, entries, open)
	raw, err := json.Marshal(tt)
	if err != nil {
		return nil, nil, errs.Wrap(errs.Internal, "build time_totals", err)
	}
	j, err := value.JSONOf(raw)
	if err != nil {
		return nil, nil, errs.Wrap(errs.Internal, "build time_totals", err)
	}
	out[fieldTimeTotals] = j
	parts := make([]string, 0, len(tt.Workers))
	for _, w := range tt.Workers {
		parts = append(parts, w.Label+" "+timeentry.FormatSeconds(w.Seconds))
	}
	msg := "time: totals only (" + strings.Join(parts, ", ") + "; " + tt.From + " to " + tt.To + ")"
	if fromPolicy {
		msg = "time: totals only (policy time.client_share = totals)"
	}
	return out, append(warns, Warning{Code: WarnTimeTotals, Field: fieldTimeTotals, Message: msg}), nil
}

// decodeTime reads the live time entries and open spans of a row's fields
// (a field that does not decode counts as none: lint reports it).
func decodeTime(fields map[string]value.Value) ([]timeentry.Entry, []timeentry.Open) {
	var entries []timeentry.Entry
	if v := fields[fieldTimeEntries]; v != nil {
		if raw, err := v.MarshalJSON(); err == nil {
			if list, derr := timeentry.Decode(raw); derr == nil {
				for _, en := range list {
					if !en.Deleted() {
						entries = append(entries, en)
					}
				}
			}
		}
	}
	var open []timeentry.Open
	if v := fields[fieldTimeOpen]; v != nil && v.Kind() != value.KindNull {
		if raw, err := v.MarshalJSON(); err == nil {
			open, _ = timeentry.DecodeOpen(raw)
		}
	}
	return entries, open
}

// timeTotals computes the per-actor totals (overlap of one actor counted
// once, timecalc 5.1; sessions folded, the client sees no sessions). Open
// spans count up to now.
func (e *Engine) timeTotals(ctx context.Context, entries []timeentry.Entry, open []timeentry.Open) TimeTotalsDoc {
	now := e.wall().UTC()
	var spans []timecalc.Span
	var tt TimeTotalsDoc
	day := func(d string) {
		if d == "" {
			return
		}
		if tt.From == "" || d < tt.From {
			tt.From = d
		}
		if d > tt.To {
			tt.To = d
		}
	}
	for _, en := range entries {
		a := en.ActorID.String()
		spans = append(spans, timecalc.Span{Worker: timecalc.Worker{Actor: a}, Owner: a, Start: en.StartTime(), End: en.EndTime(), Seconds: en.Seconds, Manual: en.Source == timeentry.SourceManual})
		day(en.Day)
	}
	for _, o := range open {
		a := o.ActorID.String()
		spans = append(spans, timecalc.Span{Worker: timecalc.Worker{Actor: a}, Owner: a, Start: o.StartTime(), End: now, Open: true})
		day(o.StartTime().Format(timeentry.DayLayout))
		day(now.Format(timeentry.DayLayout))
	}
	res := timecalc.Compute(spans)
	tt.EffortSeconds = res.Effort
	tt.Workers = make([]TotalWorker, 0, len(res.Workers))
	for _, w := range res.Workers {
		label, kind := e.workerOf(ctx, w.Worker.Actor)
		tt.Workers = append(tt.Workers, TotalWorker{Label: label, Kind: kind, Seconds: w.Total()})
	}
	slices.SortStableFunc(tt.Workers, func(a, b TotalWorker) int { return strings.Compare(a.Label, b.Label) })
	return tt
}

// workerOf is an actor's display name and kind for the client projection
// (unknownWorker / human when the actor row cannot be read).
func (e *Engine) workerOf(ctx context.Context, actor string) (string, string) {
	id, err := ids.ParseAny(actor)
	if err != nil {
		return unknownWorker, schema.ActorKindHuman
	}
	_, r, err := e.Get(ctx, Ref{Table: schema.TableActors, ID: id})
	if err != nil || r.Deleted {
		return unknownWorker, schema.ActorKindHuman
	}
	label, kind := unknownWorker, schema.ActorKindHuman
	if v := r.Fields[fieldDisplayName]; v != nil && v.String() != "" {
		label = v.String()
	}
	if v := r.Fields[fieldActorKind]; v != nil && v.String() != "" {
		kind = v.String()
	}
	return label, kind
}
