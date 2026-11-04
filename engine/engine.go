// Package engine is lore's ONE read and write path (STORE_SERVER_SPEC 3,
// 9.2, layer L4): every surface calls an operation through Run, and every
// operation reads and writes rows through this package's API, which runs, in
// order for each write: validate against the TableDef → reference check
// (DOCS O2) → secret scan with the secrets.on_detect policy → confirm →
// stamp id, HLC, audit fields and _o → store.Backend.Write with Expect →
// on ErrStale re-read, merge field by field and retry once → Cache.Apply →
// audit.
//
// Why one path: the scanner, the policy and the audit log can only be
// trusted if nothing can write around them. Backends decide nothing (spec
// 3); the cache is refreshed from the backend, never written first.
//
// No package-level state: everything is passed to New, so tests, the CLI
// and a later server each build their own Engine.
package engine

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Attached is one store the engine may read and write.
type Attached struct {
	Name     store.Source
	Backend  store.Backend
	ReadOnly bool // read_only: true in stores.yaml (spec 7.1)
	// Level is the settings level this store's policies, secret_rules and
	// secret_allows rows count at (logic/policy, O1). Empty: the repo
	// store counts at policy.LevelRepo, any other store at
	// policy.LevelStoreRoot (its rows still tighten, never loosen).
	Level policy.Level
	// Client marks a client repo (v0.1.17 routing: internal knowledge must
	// never land in it): Move refuses rows holding secret references into
	// it (BATCH 5), writes no `moves` entry there, projects actor rows (F18)
	// and makes it client-class for F15 (logic/direction). wire sets it for
	// the repo store from Options.RepoIsClient (true when unknown).
	Client bool
	// KeyNamespace is hashed into the natural id of every row created in
	// this store (the store's sto_ id, plus "/<folder>" for a folder
	// target), so the same key in two stores is two rows with two ids.
	// Empty keeps v0.1.17's bytes: the repo store, whose rows must keep
	// converging with v0.1.17 writers of the same repository.
	KeyNamespace string
	// Place is the attachment's place id recorded in `_o.moves`
	// (PROVENANCE_DESIGN 5.4, FQ4): "<sto_ id>/<folder prj_ id>" for a
	// store folder, "<sto_ id>" for a store root. Empty: the attached name
	// is recorded (the repo store, test backends).
	Place string
	// ActorAs, when set, maps the writer of a row stored in this store to
	// the actor the row is stamped with: created_by, updated_by, the HLC's
	// actor part and the audit event's actor. Why: in a client repo an
	// agent's work is recorded as its owning person (v0.1.17 D8, Q11: a
	// client never sees an agent id), while every other store keeps the
	// agent's own id. nil stamps the writer as is. It must be idempotent
	// (a mapped id maps to itself) because a row's created_by is mapped
	// again on every later write.
	ActorAs func(ids.ID[ids.Actor]) ids.ID[ids.Actor]
	// Location names where this attachment's rows physically live: the
	// canonical store url with its branch and folder (attach.Open sets it).
	// Two names over one store folder (an org store attached at its linked
	// folder as "northwind" and the same folder reached as
	// "northwind/globex") share one Location, and a lookup by id counts a
	// row found through both once (the first in stores.yaml order), so the
	// same row is never "ambiguous" with itself. Empty: a place of its own
	// (the repo store, a test backend).
	Location string
}

// stampAs applies st.ActorAs to the audit fields and the HLC actor of r
// (see Attached.ActorAs); a store without ActorAs returns r unchanged.
func stampAs(st Attached, r row.Row) row.Row {
	if st.ActorAs == nil {
		return r
	}
	if !r.CreatedBy.IsZero() {
		r.CreatedBy = st.ActorAs(r.CreatedBy)
	}
	if !r.UpdatedBy.IsZero() {
		r.UpdatedBy = st.ActorAs(r.UpdatedBy)
	}
	if !r.HLC.Actor.IsZero() {
		r.HLC.Actor = st.ActorAs(r.HLC.Actor)
	}
	r.Fields = actorFieldsAs(st.ActorAs, r.Fields)
	return r
}

// actorFieldsAs maps every top-level field that holds exactly one actor id
// (a Ref to an actors row, or a Text field such as tasks.done_by or
// approved_by that stores the id as text) through as, keeping the field's
// kind. Why: the envelope is not the only place a writer's id lands —
// task done records done_by, --assign @me records assigned_to_actor_id —
// and in a client repo every one of them must name the agent's owner, not
// the agent (D8, Q11), or the client's committed files carry the agent id.
// The map is copied before the first change, so the caller's row is never
// mutated; a field whose value as leaves unchanged is left as it was.
func actorFieldsAs(as func(ids.ID[ids.Actor]) ids.ID[ids.Actor], fields map[string]value.Value) map[string]value.Value {
	out, copied := fields, false
	for k, v := range fields {
		if v == nil || (v.Kind() != value.KindText && v.Kind() != value.KindRef) {
			continue
		}
		id, err := ids.Parse[ids.Actor](v.String())
		if err != nil || id.IsZero() {
			continue
		}
		mapped := as(id)
		if mapped == id || mapped.IsZero() {
			continue
		}
		var nv value.Value
		if v.Kind() == value.KindRef {
			ref, rerr := value.RefOf(mapped.Any())
			if rerr != nil {
				continue
			}
			nv = ref
		} else {
			txt, terr := value.TextOf(mapped.String())
			if terr != nil {
				continue
			}
			nv = txt
		}
		if !copied {
			out, copied = maps.Clone(fields), true
		}
		out[k] = nv
	}
	return out
}

// IDSource issues permanent row ids. The default makes UUIDv7 ids (the
// model/ids form); tests inject a fixed sequence.
type IDSource interface {
	NewID(p ids.Prefix) (ids.AnyID, error)
}

// Engine runs operations and their reads and writes. Safe for concurrent
// use: it holds no mutable state of its own beyond the clock, which locks.
type Engine struct {
	stores    map[store.Source]Attached
	order     []store.Source // stores.yaml order: precedence for lookups
	target    store.Source   // default write target
	cache     store.Cache
	reg       *schema.Registry
	scanner   scan.Scanner
	settings  Settings
	clock     *hlc.Clock
	wall      func() time.Time
	ids       IDSource
	actor     ids.ID[ids.Actor]
	audit     AuditSink
	confirmer Confirmer
	prov      Provenance // nil: _o is {cmd, store}, no _w (WithProvenance)
}

// Option configures New.
type Option func(*Engine) error

// WithStore attaches a store. Names must be snake_case and unique; the
// repo's own store is store.SourceRepo.
func WithStore(a Attached) Option {
	return func(e *Engine) error {
		if !validStoreName(string(a.Name)) || a.Backend == nil {
			return errs.New(errs.ConfigInvalid, "engine: a store needs a snake_case name (or <name>/<folder>) and a backend")
		}
		if _, dup := e.stores[a.Name]; dup {
			return errs.New(errs.ConfigInvalid, "engine: store "+string(a.Name)+" attached twice")
		}
		e.stores[a.Name] = a
		e.order = append(e.order, a.Name)
		return nil
	}
}

// storeRootFolder is the folder name of a store's root (rowfs.FolderRoot):
// `<store>/.` is the root of a store attached elsewhere at a folder.
const storeRootFolder = "."

// validStoreName accepts an attached store name `<snake>`, `<snake>/.`
// (the store root) or `<snake>/<folder>` (STORE TARGETS: `--to agency/org` is the company
// folder of store agency). A folder target is attached under exactly the
// name the person types, so --to needs no second parser. Folder segments
// must be non-empty, never "." or "..", and use only lower-case letters,
// digits, '_' and '-' (no backslash, no path tricks).
func validStoreName(n string) bool {
	base, rest, hasFolder := strings.Cut(n, "/")
	if !row.ValidName(base) {
		return false
	}
	if !hasFolder || rest == storeRootFolder {
		return true
	}
	for seg := range strings.SplitSeq(rest, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
		for _, c := range seg {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
				return false
			}
		}
	}
	return true
}

// WithDefaultTarget names the store a write goes to when the operation names
// none (CONFIG_GUIDE default target rules are applied by the caller). The
// default is store.SourceRepo when attached, else the first store.
func WithDefaultTarget(name store.Source) Option {
	return func(e *Engine) error { e.target = name; return nil }
}

// WithCache sets the cache port (required).
func WithCache(c store.Cache) Option { return func(e *Engine) error { e.cache = c; return nil } }

// WithRegistry sets the table definitions (required).
func WithRegistry(r *schema.Registry) Option { return func(e *Engine) error { e.reg = r; return nil } }

// WithScanner sets the secret scanner (required). A scanner that also
// implements scan.Reporter has its degraded scans turned into warnings.
func WithScanner(s scan.Scanner) Option { return func(e *Engine) error { e.scanner = s; return nil } }

// WithSettings sets where policy rows, secret rule states and allow entries
// come from. Without it they are the policies, secret_rules and
// secret_allows rows of every attached store, each at its store's level
// (storeSettings); with no such rows the built-in defaults apply
// (secrets.on_detect: ask).
func WithSettings(s Settings) Option { return func(e *Engine) error { e.settings = s; return nil } }

// WithActor sets the actor every write is stamped with (required).
func WithActor(a ids.ID[ids.Actor]) Option { return func(e *Engine) error { e.actor = a; return nil } }

// WithWall sets the wall clock (default time.Now); the HLC clock reads it.
func WithWall(wall func() time.Time) Option {
	return func(e *Engine) error { e.wall = wall; return nil }
}

// WithIDs sets the id source (default: fresh UUIDv7 ids).
func WithIDs(s IDSource) Option { return func(e *Engine) error { e.ids = s; return nil } }

// WithAudit sets the audit sink (required): every write is recorded.
func WithAudit(s AuditSink) Option { return func(e *Engine) error { e.audit = s; return nil } }

// WithConfirmer lets an interactive caller answer questions (a terminal
// prompt). Without it every question is E_NEEDS_CONFIRM unless the caller
// passed yes.
func WithConfirmer(c Confirmer) Option { return func(e *Engine) error { e.confirmer = c; return nil } }

// New builds an Engine. Cache, registry, scanner, actor, audit sink and at
// least one store are required; a missing one is E_CONFIG_INVALID listing
// them all, so a wiring mistake fails at startup.
func New(opts ...Option) (*Engine, error) {
	e := &Engine{stores: map[store.Source]Attached{}, wall: time.Now, ids: uuidIDs{}}
	for _, o := range opts {
		if err := o(e); err != nil {
			return nil, err
		}
	}
	var missing []string
	for _, m := range []struct {
		name string
		ok   bool
	}{
		{"cache", e.cache != nil}, {"registry", e.reg != nil}, {"scanner", e.scanner != nil},
		{"actor", !e.actor.IsZero()}, {"audit sink", e.audit != nil}, {"a store", len(e.stores) > 0},
		{"wall clock", e.wall != nil}, {"id source", e.ids != nil},
	} {
		if !m.ok {
			missing = append(missing, m.name)
		}
	}
	if len(missing) > 0 {
		return nil, errs.New(errs.ConfigInvalid, "engine: missing "+strings.Join(missing, ", "))
	}
	if e.target == "" {
		e.target = e.order[0]
		if slices.Contains(e.order, store.SourceRepo) {
			e.target = store.SourceRepo
		}
	}
	if _, ok := e.stores[e.target]; !ok {
		return nil, errs.New(errs.ConfigInvalid, "engine: default target "+string(e.target)+" is not attached")
	}
	if e.settings == nil {
		e.settings = storeSettings{e: e}
	}
	e.clock = hlc.NewClock(e.actor, e.wall)
	return e, nil
}

// Stores lists the attached store names in precedence order.
func (e *Engine) Stores() []store.Source { return slices.Clone(e.order) }

// IsClient reports whether the attached store name is a client repo
// (Attached.Client); false for an unknown name.
func (e *Engine) IsClient(name store.Source) bool { return e.stores[name].Client }

// Registry returns the table definitions the engine validates against.
func (e *Engine) Registry() *schema.Registry { return e.reg }

// Settings is where the engine reads the settings rows that govern a write
// to one store, resolved over every level the repo sees (logic/policy).
type Settings interface {
	// Policy returns the effective policy and secret rule states for a
	// write to target.
	Policy(target store.Source) (policy.Set, policy.Rules, error)
	// Allows returns the "not a secret" entries that apply to target.
	Allows(target store.Source) ([]scan.Allow, error)
}

// uuidIDs is the default IDSource.
type uuidIDs struct{}

// idSep joins the prefix and the 32 hex digits of an id.
const idSep = "_"

// NewID implements IDSource.
func (uuidIDs) NewID(p ids.Prefix) (ids.AnyID, error) {
	u, err := uuid.NewV7()
	if err != nil {
		return ids.AnyID{}, errs.Wrap(errs.Internal, "engine: no random source for a new id", err)
	}
	return ids.ParseAny(string(p) + idSep + strings.ReplaceAll(u.String(), "-", ""))
}

// attached returns the named store.
func (e *Engine) attached(name store.Source) (Attached, error) {
	a, ok := e.stores[name]
	if !ok {
		return Attached{}, errs.New(errs.NotFound, "engine: no store named "+string(name)+" is attached")
	}
	return a, nil
}

// writable returns the named store when a write may go to it (spec 9.2
// permission, 0.1.18: attached and not read_only).
func (e *Engine) writable(name store.Source) (Attached, error) {
	a, err := e.attached(name)
	if err != nil {
		return Attached{}, err
	}
	if a.ReadOnly {
		return Attached{}, errs.Wrap(errs.StoreReadOnly, "engine: store "+string(name)+" is attached read-only", store.ErrReadOnly).
			WithHint("write to another store with --to")
	}
	return a, nil
}

// tableDef returns the definition of a table rows may be written to.
func (e *Engine) tableDef(t row.Table) (schema.TableDef, error) {
	def, ok := e.reg.Table(t)
	if !ok {
		return schema.TableDef{}, errs.New(errs.NotFound, "engine: no table "+string(t))
	}
	if !def.Synced {
		return schema.TableDef{}, errs.New(errs.InvalidInput, "engine: "+string(t)+" is a cache projection, not stored rows")
	}
	if !def.Kind.IsZero() {
		// Kind rows live in `resources` with their fields in one json field;
		// the resource noun (phase 2) maps them. Refused loudly until then.
		return schema.TableDef{}, errs.New(errs.NotImplemented, "engine: write resources of kind "+string(t)+" through the resources table")
	}
	return def, nil
}
