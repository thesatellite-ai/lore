// Package profilestoretest is an in-memory profilestore.Store for the
// profile ops' unit tests: the rows live in a map, writes are validated
// against the built-in schema (logic/validate) and get the engine's
// natural id rule (TS-5), and the caller checks of Store.Roster are
// switches on the fake. It is a test double: no scanning, no audit, no
// merge, no store routing; the wire tests cover the real path.
package profilestoretest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/logic/validate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Fake defaults.
const (
	// DefaultSource is the org roster store a Fake answers with.
	DefaultSource store.Source = "agency/org"
	// DefaultWhere names it in messages.
	DefaultWhere = "agency org/"
	// fakeIDTime is the fixed 48-bit time part of the fake's random ids.
	fakeIDTime = 0x01890a5d8a2b
	// fieldHandle and fieldStableKey are actors columns ResolveActor reads.
	fieldHandle    = "handle"
	fieldStableKey = "stable_key"
	handleMark     = "@"
	actorPrefix    = string(ids.PrefixActor) + "_"
)

// Fake is an in-memory profilestore.Store. The zero value is not usable;
// call New. Exported fields may be set before the first call.
type Fake struct {
	// CallerID is Roster.Caller.
	CallerID ids.ID[ids.Actor]
	// Now is the wall clock; Today is its day.
	Now time.Time
	// Agent makes the caller an agent: write rosters are refused with
	// E_POLICY_DENIED unless MayManagePeople (TS-4).
	Agent           bool
	MayManagePeople bool
	// NoOrg refuses write rosters with E_REFUSED (TS-1); Client refuses
	// every roster with E_LEAK_BLOCKED.
	NoOrg  bool
	Client bool
	// WarnOnWrite, when set, is a warning line every successful write
	// returns (the engine's W_… warnings; Roster.Warnings).
	WarnOnWrite string
	// Pushes counts Push calls; Opened counts Roster calls; Closed counts
	// done calls.
	Pushes, Opened, Closed int

	mu   sync.Mutex
	rows map[row.Table]map[ids.AnyID]row.Row
	defs map[row.Table]schema.TableDef
	seq  int
}

var _ profilestore.Store = (*Fake)(nil)

// New returns an empty fake whose caller is caller, at now.
func New(caller ids.ID[ids.Actor], now time.Time) *Fake {
	defs := map[row.Table]schema.TableDef{}
	for _, d := range schema.Builtin() {
		defs[d.Name] = d
	}
	return &Fake{CallerID: caller, Now: now, rows: map[row.Table]map[ids.AnyID]row.Row{}, defs: defs}
}

// Put stores r as is (a seed: actors, projects, rows of an older lore).
func (f *Fake) Put(r row.Row) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows[r.Table] == nil {
		f.rows[r.Table] = map[ids.AnyID]row.Row{}
	}
	f.rows[r.Table][r.ID] = r
}

// All returns every stored row of table, deleted ones included, by id.
func (f *Fake) All(table row.Table) []row.Row {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sorted(table, true)
}

// Roster implements profilestore.Store.
func (f *Fake) Roster(_ context.Context, write bool) (profilestore.Roster, func(), error) {
	f.mu.Lock()
	f.Opened++
	f.mu.Unlock()
	done := func() {
		f.mu.Lock()
		f.Closed++
		f.mu.Unlock()
	}
	switch {
	case f.Client:
		return nil, done, errs.New(errs.LeakBlocked, "roster: "+string(DefaultSource)+" is a client repo; the roster lives in the org's store")
	case write && f.NoOrg:
		return nil, done, errs.New(errs.Refused, "team profiles live in an org store").WithHint("lore org create <name>")
	case write && f.Agent && !f.MayManagePeople:
		return nil, done, errs.New(errs.PolicyDenied, "agents.may_manage_people is false: an agent may not change team profiles").
			WithHint("a person runs it in their own terminal")
	}
	return roster{f: f, warns: &[]string{}}, done, nil
}

// roster is the opened fake; warns are its writes' warnings (Warnings).
type roster struct {
	f     *Fake
	warns *[]string
}

// warned records WarnOnWrite after a write that succeeded, as the engine
// returns a warning with the row; err passes through.
func (r roster) warned(err error) error {
	if err == nil && r.f.WarnOnWrite != "" {
		*r.warns = append(*r.warns, r.f.WarnOnWrite)
	}
	return err
}

// Warnings implements profilestore.Roster.
func (r roster) Warnings() []string { return slices.Clone(*r.warns) }

func (r roster) Source() store.Source      { return DefaultSource }
func (r roster) Where() string             { return DefaultWhere }
func (r roster) Caller() ids.ID[ids.Actor] { return r.f.CallerID }
func (r roster) Today() time.Time {
	y, m, d := r.f.Now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
func (r roster) Push(context.Context) { r.f.mu.Lock(); r.f.Pushes++; r.f.mu.Unlock() }
func (r roster) def(t row.Table) (schema.TableDef, error) {
	d, ok := r.f.defs[t]
	if !ok {
		return schema.TableDef{}, errs.New(errs.InvalidInput, "fake: no table "+string(t))
	}
	return d, nil
}

// List implements profilestore.Roster.
func (r roster) List(_ context.Context, table row.Table) ([]row.Row, error) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	return r.f.sorted(table, false), nil
}

// sorted is table's rows by id; live only unless all. Caller holds mu.
func (f *Fake) sorted(table row.Table, all bool) []row.Row {
	out := make([]row.Row, 0, len(f.rows[table]))
	for _, x := range f.rows[table] {
		if all || live(x) {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, func(a, b row.Row) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	return out
}

// live: not a tombstone and not in the trash.
func live(x row.Row) bool {
	_, trashed := x.Fields[trash.FieldDeletedReason]
	return !x.Deleted && !trashed
}

// Get implements profilestore.Roster.
func (r roster) Get(_ context.Context, table row.Table, id ids.AnyID) (row.Row, error) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	x, ok := r.f.rows[table][id]
	if !ok {
		return row.Row{}, errs.New(errs.NotFound, "no "+string(table)+" row "+id.String())
	}
	return x, nil
}

// Create implements profilestore.Roster: the natural id of the first
// UniqueOn when every key field is set (TS-5); a live row with that key
// is "already exists" (E_NAME_TAKEN), as in the engine.
func (r roster) Create(ctx context.Context, table row.Table, fields map[string]value.Value) (row.Row, error) {
	x, err := r.create(ctx, table, ids.AnyID{}, fields)
	return x, r.warned(err)
}

// CreateWithID implements profilestore.Roster.
func (r roster) CreateWithID(ctx context.Context, table row.Table, id ids.AnyID, fields map[string]value.Value) (row.Row, error) {
	if id.IsZero() {
		return row.Row{}, errs.New(errs.InvalidID, "fake: CreateWithID needs an id")
	}
	x, err := r.create(ctx, table, id, fields)
	return x, r.warned(err)
}

func (r roster) create(_ context.Context, table row.Table, pin ids.AnyID, fields map[string]value.Value) (row.Row, error) {
	def, err := r.def(table)
	if err != nil {
		return row.Row{}, err
	}
	typed, err := validate.Fields(def, fields, validate.Whole)
	if err != nil {
		return row.Row{}, err
	}
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	id := pin
	if id.IsZero() {
		if keys, parts, ok := naturalKey(def, typed); ok {
			nid, nerr := ids.Natural(def.Prefix, parts...)
			if nerr != nil {
				return row.Row{}, errs.Wrap(errs.Internal, "fake: natural id", nerr)
			}
			for _, x := range r.f.rows[table] {
				if live(x) && sameKey(x.Fields, typed, keys) {
					return row.Row{}, errs.New(errs.NameTaken, "fake: a "+string(table)+" row with this "+strings.Join(keys, ", ")+" already exists ("+x.ID.String()+")")
				}
			}
			if _, taken := r.f.rows[table][nid]; !taken {
				id = nid
			}
		}
	}
	if id.IsZero() {
		if id, err = r.f.nextID(def.Prefix); err != nil {
			return row.Row{}, err
		}
	}
	if x, ok := r.f.rows[table][id]; ok && live(x) {
		return row.Row{}, errs.New(errs.InvalidInput, "fake: "+id.String()+" already exists")
	}
	x := row.Row{Table: table, ID: id, Fields: typed, CreatedAt: r.f.Now, CreatedBy: r.f.CallerID, UpdatedAt: r.f.Now, UpdatedBy: r.f.CallerID}
	if r.f.rows[table] == nil {
		r.f.rows[table] = map[ids.AnyID]row.Row{}
	}
	r.f.rows[table][id] = x
	return x, nil
}

// nextID is a fresh, valid, deterministic id. Caller holds mu.
func (f *Fake) nextID(p ids.Prefix) (ids.AnyID, error) {
	f.seq++
	return ids.ParseAny(fmt.Sprintf("%s_%012x70008%015x", p, fakeIDTime, f.seq))
}

// Update implements profilestore.Roster: value.Null clears a field.
func (r roster) Update(ctx context.Context, table row.Table, id ids.AnyID, patch map[string]value.Value) (row.Row, error) {
	x, err := r.update(ctx, table, id, patch)
	return x, r.warned(err)
}

func (r roster) update(_ context.Context, table row.Table, id ids.AnyID, patch map[string]value.Value) (row.Row, error) {
	def, err := r.def(table)
	if err != nil {
		return row.Row{}, err
	}
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	cur, ok := r.f.rows[table][id]
	if !ok || !live(cur) {
		return row.Row{}, errs.New(errs.NotFound, "no live "+string(table)+" row "+id.String())
	}
	next := make(map[string]value.Value, len(cur.Fields)+len(patch))
	for k, v := range cur.Fields {
		next[k] = v
	}
	for k, v := range patch {
		if v == nil || v.Kind() == value.KindNull {
			delete(next, k)
			continue
		}
		next[k] = v
	}
	typed, err := validate.Fields(def, next, validate.Whole)
	if err != nil {
		return row.Row{}, err
	}
	cur.Fields, cur.UpdatedAt, cur.UpdatedBy = typed, r.f.Now, r.f.CallerID
	r.f.rows[table][id] = cur
	return cur, nil
}

// Delete implements profilestore.Roster: the row goes to the (fake) trash;
// actors are refused (BATCH2 #8).
func (r roster) Delete(ctx context.Context, table row.Table, id ids.AnyID, reason string) error {
	return r.warned(r.del(ctx, table, id, reason))
}

func (r roster) del(_ context.Context, table row.Table, id ids.AnyID, reason string) error {
	if table == schema.TableActors {
		return errs.New(errs.Refused, "actors are never deleted").WithHint("lore actor offboard")
	}
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	cur, ok := r.f.rows[table][id]
	if !ok || !live(cur) {
		return errs.New(errs.NotFound, "no live "+string(table)+" row "+id.String())
	}
	t, err := value.TextOf(reason)
	if err != nil {
		return errs.Wrap(errs.InvalidInput, "fake: reason", err)
	}
	cur.Fields[trash.FieldDeletedReason] = t
	r.f.rows[table][id] = cur
	return nil
}

// InTrash implements profilestore.Roster: E_IN_TRASH for a row Delete
// moved to the (fake) trash, nil for a live, purged or unknown row.
func (r roster) InTrash(_ context.Context, table row.Table, id ids.AnyID) error {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	x, ok := r.f.rows[table][id]
	if _, trashed := x.Fields[trash.FieldDeletedReason]; ok && trashed && !x.Deleted {
		return trash.InTrashErr(x)
	}
	return nil
}

// Purge empties id of table out of the (fake) trash: a tombstone without
// the delete fields, as `lore trash purge` leaves it.
func (f *Fake) Purge(table row.Table, id ids.AnyID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	x, ok := f.rows[table][id]
	if !ok {
		return
	}
	fields := make(map[string]value.Value, len(x.Fields))
	for k, v := range x.Fields {
		if !slices.Contains(trash.Fields(), k) {
			fields[k] = v
		}
	}
	x.Fields, x.Deleted = fields, true
	f.rows[table][id] = x
}

// ResolveActor implements profilestore.Roster: @handle (active first), an
// act_ id, or a stable key.
func (r roster) ResolveActor(_ context.Context, ref string) (row.Row, error) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	actors := r.f.sorted(schema.TableActors, false)
	var hits []row.Row
	switch {
	case strings.HasPrefix(ref, handleMark):
		h := strings.TrimPrefix(ref, handleMark)
		for _, a := range actors {
			if text(a, fieldHandle) == h {
				hits = append(hits, a)
			}
		}
		if len(hits) > 1 {
			var active []row.Row
			for _, a := range hits {
				if text(a, schema.FieldActorActive) != "false" {
					active = append(active, a)
				}
			}
			if len(active) == 1 {
				hits = active
			}
		}
	case strings.HasPrefix(ref, actorPrefix):
		for _, a := range actors {
			if a.ID.String() == ref {
				hits = append(hits, a)
			}
		}
	default:
		for _, a := range actors {
			if text(a, fieldStableKey) == ref {
				hits = append(hits, a)
			}
		}
	}
	switch len(hits) {
	case 0:
		return row.Row{}, errs.New(errs.NotFound, "no actor "+ref+" on "+DefaultWhere+"'s roster")
	case 1:
		return hits[0], nil
	}
	names := make([]string, 0, len(hits))
	for _, h := range hits {
		names = append(names, h.ID.String())
	}
	return row.Row{}, errs.New(errs.AmbiguousID, "actor "+ref+" matches "+strings.Join(names, ", ")).WithHint("name one by its act_ id")
}

// naturalKey mirrors engine naturalKey: the first unique index whose
// fields are all set.
func naturalKey(def schema.TableDef, fields map[string]value.Value) ([]string, []string, bool) {
	for _, ix := range def.Indexes {
		if !ix.Unique || len(ix.Fields) == 0 {
			continue
		}
		parts := make([]string, 0, len(ix.Fields))
		for _, k := range ix.Fields {
			v, ok := fields[k]
			if !ok || v.Kind() == value.KindNull || v.String() == "" {
				return nil, nil, false
			}
			parts = append(parts, v.String())
		}
		return ix.Fields, parts, true
	}
	return nil, nil, false
}

// sameKey mirrors engine sameKey.
func sameKey(a, b map[string]value.Value, keys []string) bool {
	for _, k := range keys {
		x, okx := a[k]
		y, oky := b[k]
		if !okx || !oky || x.String() != y.String() {
			return false
		}
	}
	return true
}

// text is a field's text, "" when absent.
func text(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}
