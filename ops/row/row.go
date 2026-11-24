package row

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/factstale"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/metashow"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/resmask"
	"github.com/thesatellite-ai/lore/ops/internal/showfield"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (`lore row show|move`, mounted as `lore show|move`).
const (
	Noun   = "row"
	OpShow = "row.show"
	OpMove = "row.move"
	// hidden is what a sensitive field shows (RQ1: hidden in show).
	hidden = "(sensitive, hidden)"
	// showSensitiveField is the json name of ShowIn.ShowSensitive: its
	// true value makes row.show ask first (META 3.20, XQ3).
	showSensitiveField = "show_sensitive"
	timeLayout         = "2006-01-02 15:04 UTC"
	shortID            = 12 // how much of a machine id the human form prints
)

// Fields read from the lookup tables and from rows written before 0.1.18.
const (
	fDisplayName = "display_name"
	fName        = "name"
	fMountName   = "mount_name"
	fLabel       = "label"
	fKind        = "kind"
	fOriginURL   = "origin_url"
	fSourceKind  = "source_kind"
	fSourceRef   = "source_ref"
)

// Env is what the row operations read; *wire.App implements it.
type Env interface {
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
	TableSchema(ctx context.Context, table row.Table) (schema.TableDef, bool)
}

// Mover is the Env upgrade row.move needs (wire.App.MoveWith). An Env
// without it answers E_UNSUPPORTED instead of pretending.
type Mover interface {
	MoveWith(ctx context.Context, ref engine.Ref, to store.Source, opt engine.MoveOptions) (engine.Written, error)
}

// ShowIn is the input of row.show.
type ShowIn struct {
	// --store <store> (devco D-499): the read names its store.
	op.StoreFlag
	ID string `json:"id" arg:"0" desc:"any row id (mem_…, tsk_…, res_…, var_…)"`
	// Origin adds the origin blocks to the text form; --json always
	// carries them (D-455), so it is accepted there and changes nothing.
	Origin bool `json:"origin,omitempty" flag:"origin" desc:"also print where the row came from: _o, the audit fields, the _w stamp and its moves (--json always has them)"`
	// ShowSensitive: META 3.20 / RQ1 (asks first; --yes without a
	// terminal, XQ3; agents may run it too, OQ25).
	ShowSensitive bool `json:"show_sensitive,omitempty" flag:"show-sensitive" desc:"print sensitive field and metadata values (asks first)"`
	expand.Flag
	expand.RevealFlag
}

// MoveIn is the input of row.move.
type MoveIn struct {
	ID string `json:"id,omitempty" arg:"0" desc:"any row id (or --from for every row of a store or folder)"`
	To string `json:"to" flag:"to" desc:"the target store: repo | me | <store>[/<folder>]"`
	// Bulk form (devco D-712): every matching row of one store or folder,
	// one write at --to and one at the source (move_many.go).
	BulkFlags
	DropInternal bool   `json:"drop_internal,omitempty" flag:"drop-internal" desc:"into a client repo: remove the row's internal metadata values instead of refusing"`
	WithTime     string `json:"with_time,omitempty" flag:"with-time" desc:"totals | none: per-worker time totals for a task moved into a client repo (default: the time.client_share policy, none)"`
	WithChildren bool   `json:"with_children,omitempty" flag:"with-children" desc:"a task: move its whole subtree too; every sub-task keeps its id and parent"`
}

// Actor is an actor as show names it.
type Actor struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Kind        string `json:"kind,omitempty"`
}

// Named is an id with its current label ("" when not on this machine).
type Named struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Git is `_o.git`, with Reason: the state in words, so "none" says why
// no branch or commit was recorded (D-456; read-time, never stored).
type Git struct {
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// The `_o.git.state` values (logic/origin.GitState) show explains.
const (
	gitStateDetached = string(origin.GitDetached)
	gitStateUnborn   = string(origin.GitUnborn)
	gitStateNone     = string(origin.GitNone)
)

// gitReason is the state of g in words. "none" has two causes, told
// apart by what else `_o` holds: a create with no code repo open (a store
// folder, a folder outside any repo: no repo id, no dir) records no git
// facts at all; a create in a lore place that is no git repository (or
// with git missing) has its repo id.
func gitReason(g Git, repo string) string {
	switch g.State {
	case gitStateDetached:
		return "HEAD pointed at a commit, not a branch (a CI checkout, a bisect or a rebase)"
	case gitStateUnborn:
		return "the repo had no commit yet"
	case gitStateNone:
		if repo == "" {
			return "written from a store folder or a folder outside any code repo, not a code repo: there was no branch or commit to record"
		}
		return "the folder was not a git repository, or git could not run, when the row was written"
	}
	return ""
}

// Import is `_o.import`.
type Import struct {
	Source string `json:"source,omitempty"`
	Src    string `json:"src,omitempty"`
	ExtID  string `json:"ext_id,omitempty"`
}

// Move is one `_o.moves` entry (PROV 5.4).
type Move struct {
	At        string `json:"at"`
	By        string `json:"by"`
	MovedFrom string `json:"moved_from"`
	MovedTo   string `json:"moved_to"`
}

// OriginBlock is `_o` as show prints it; Recorded is false for a row
// written before 0.1.18 (never guessed, PROV 9.1). Store names the store
// like Folder names the folder (D-456: the sto_ id alone told a reader
// nothing); Version and Machine are the `_w` stamp of the row's last
// write, repeated here so one block says where and with what the row was
// written (the `write` block keeps them too).
type OriginBlock struct {
	Recorded bool   `json:"recorded"`
	Cmd      string `json:"cmd,omitempty"`
	Store    Named  `json:"store,omitzero"`
	Folder   Named  `json:"folder,omitzero"`
	Repo     Named  `json:"repo,omitzero"`
	Project  Named  `json:"project,omitzero"`
	Dir      string `json:"dir,omitempty"`
	Git      Git    `json:"git,omitzero"`
	Import   Import `json:"import,omitzero"`
	Version  string `json:"version,omitempty"`
	Machine  Named  `json:"machine,omitzero"`
}

// Referrer is a live row whose fields name the shown row: its relations
// seen from the other side (a comment, a link, a sub-task, a tag use).
type Referrer struct {
	Store  string   `json:"store"`
	Table  string   `json:"table"`
	ID     string   `json:"id"`
	Fields []string `json:"fields"`
}

// StoreNamer is the Env upgrade naming a store by its sto_ id
// (`<org>/<store>`, the clone's place); wire.App implements it. Without
// it the origin store carries its id only.
type StoreNamer interface {
	StoreName(ctx context.Context, id string) (string, bool)
}

// Referrers is the Env upgrade listing the rows whose fields name id
// (engine.Referrers); wire.App implements it. Without it show lists no
// referrers.
type Referrers interface {
	Referrers(ctx context.Context, id ids.AnyID) ([]engine.Referrer, error)
}

// Audit is the row's audit fields.
type Audit struct {
	CreatedAt string `json:"created_at,omitempty"`
	CreatedBy Actor  `json:"created_by,omitzero"`
	UpdatedAt string `json:"updated_at,omitempty"`
	UpdatedBy Actor  `json:"updated_by,omitzero"`
}

// Write is the `_w` stamp.
type Write struct {
	Version string `json:"version,omitempty"`
	Machine Named  `json:"machine,omitzero"`
}

// Lineage is the row's moves and copy pointers.
type Lineage struct {
	CopiedFrom string `json:"copied_from,omitempty"`
	Moves      []Move `json:"moves"`
}

// Legacy is the v0.1.17 source fields, shown as they are.
type Legacy struct {
	SourceKind string `json:"source_kind,omitempty"`
	SourceRef  string `json:"source_ref,omitempty"`
}

// Origin is the --origin part of show, before ShowOut spreads it into
// its own keys.
type Origin struct {
	Origin  OriginBlock
	Audit   Audit
	Write   Write
	Lineage Lineage
	Legacy  Legacy
}

// ShowOut is the output of row.show.
type ShowOut struct {
	ID    string `json:"id"`
	Table string `json:"table"`
	Store string `json:"store"`
	// Place is Store as the header line prints it: "<store>/<folder>" for
	// a store attached at a folder (placeLabel). Text only; --json keeps
	// Store, the name --store takes.
	Place string `json:"-"`
	// Fields are the row's fields; a list field (watchers, assignees,
	// tag_ids) stays a JSON array (D-455).
	Fields showfield.Map     `json:"fields"`
	Meta   map[string]string `json:"meta,omitempty"`
	// MetaMasked names the metadata keys and the sensitive fields printed
	// masked (META 3.14/3.20 meta_masked); empty with --show-sensitive.
	MetaMasked []string `json:"meta_masked,omitempty"`
	// The origin blocks (PROV 9.1's JSON keys), always filled (D-455:
	// --json prints everything the row has; the text form prints them
	// with --origin). (The session block lives only on the writing
	// machine's sessions table, AID 6.6, so show never carries it.)
	Origin  OriginBlock `json:"origin,omitzero"`
	Audit   Audit       `json:"audit,omitzero"`
	Write   Write       `json:"write,omitzero"`
	Lineage Lineage     `json:"lineage,omitzero"`
	Legacy  Legacy      `json:"legacy,omitzero"`
	// Tags are the row's tag_ids with their names; ReferencedBy the live
	// rows whose fields name this row (D-455).
	Tags         []Named    `json:"tags,omitempty"`
	ReferencedBy []Referrer `json:"referenced_by,omitempty"`
	// Expanded is each text field that --expand changed, filled in
	// (SECRETS 12: "--json adds expanded and refs"); Refs lists every
	// reference met, with its value or why it stayed as written.
	// A resource row's own text values are keyed "fields.<name>".
	Expanded map[string]string `json:"expanded,omitempty"`
	Refs     []refpoint.Used   `json:"refs,omitempty"`
	// Revealed, Forced and Warning: --reveal (SECRETS 2.7, SQ10).
	Revealed int    `json:"revealed,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	Warning  string `json:"warning,omitempty"`
	// CreatedAt / UpdatedAt / CreatedBy / UpdatedBy are
	// the row envelope's writers (INTEG-AUDIT-1/2): `lore show <id>` is the
	// one show of rows that have no show of their own (a comment, a commit
	// link, a tag, a knowledge link), so it prints the writers line too.
	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	CreatedBy string    `json:"created_by,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	// Stale is set when the row states a fact about code whose evidence
	// (fact_evidence) changed in git after it was verified
	// (fact_verified_at), Reverify the command that clears it: the same
	// words lore lint, search and ask print (devco:D-953, bug hunt H-212).
	Stale    string `json:"stale,omitempty"`
	Reverify string `json:"reverify,omitempty"`
	prov     *Origin
}

// FactEnv is the Env upgrade that checks a row's fact evidence against the
// code repository the command runs in (*wire.App); FactChecker is nil
// outside a git repository, and show then marks nothing.
type FactEnv interface {
	FactChecker(ctx context.Context) *factstale.Checker
}

// markStale sets out.Stale and out.Reverify when r's fact evidence changed
// after it was verified (devco:D-953).
func (o ops) markStale(ctx context.Context, src store.Source, r row.Row, out *ShowOut) {
	cl, ok := factstale.ClaimOf(r)
	if !ok || !cl.Checkable() {
		return
	}
	fe, ok := upgrade.As[FactEnv](o.env)
	if !ok {
		return
	}
	chk := fe.FactChecker(ctx)
	if chk == nil {
		return
	}
	v := chk.Check(ctx, cl)
	st := ""
	if src != store.SourceRepo {
		st = string(src)
	}
	out.Stale, out.Reverify = v.Note(), factstale.Reverify(out.ID, st, v, opclock.Now(o.env))
}

// MoveOut is the output of row.move.
type MoveOut struct {
	ID       string   `json:"id"`
	Table    string   `json:"table"`
	To       string   `json:"to"`
	Warnings []string `json:"warnings,omitempty"`
	// Notes are the client-move lines of TQ3 ("kept back: time (12
	// entries)", "time: totals only (…)"): expected, not warnings.
	Notes []string `json:"notes,omitempty"`
	// Children are the sub-tasks --with-children moved, parents first.
	Children []string `json:"children,omitempty"`
	// From is the --from store or folder of a bulk move ("" for one row);
	// Rows are the rows it moved (or, with DryRun, would move), each with
	// the store it left (devco D-712).
	From   string     `json:"from,omitempty"`
	Rows   []MovedRow `json:"rows,omitempty"`
	DryRun bool       `json:"dry_run,omitempty"`
	// Duplicates are the rows a move brings that repeat a row --to
	// already holds (devco D-724, the shape of store.adopt's; the one-row
	// move too since D-854, with its --with-children sub-tasks): listed,
	// both kept, never merged; the bulk dry run lists them too.
	Duplicates []opsstores.NearDuplicate `json:"duplicates,omitempty"`
	// next is the bulk move's printed next step (the run of a dry run).
	next string
	// place is the one-row move's full --to (a linked folder as
	// <store>/<folder>), the --store its printed near-duplicate commands
	// read.
	place string
	// setup is the " --setup <name>" the printed commands carry ("" none).
	setup string
}

// Text is the human form of a move.
func (o MoveOut) Text() string {
	if o.From != "" {
		return o.bulkText()
	}
	var b strings.Builder
	b.WriteString("✓ " + o.ID + " moved to store " + o.To + " (same id; the old copy is tombstoned)\n")
	if len(o.Children) > 0 {
		b.WriteString("  with " + strconv.Itoa(len(o.Children)) + " sub-task(s), same ids: " + strings.Join(o.Children, ", ") + "\n")
	}
	for _, n := range o.Notes {
		b.WriteString("  " + n + "\n")
	}
	for _, w := range o.Warnings {
		b.WriteString("  warning: " + w + "\n")
	}
	// D-854: the pairs as the bulk move prints them (the moved row's bare
	// id names one live row once moved)
	place := o.place
	if place == "" {
		place = o.To
	}
	for _, l := range (opsstores.NearDupPrint{Place: place, Suffix: o.setup}).Lines(o.Duplicates) {
		b.WriteString(l + "\n")
	}
	return b.String()
}

// Ops returns the row operations bound to env.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		// --reveal may answer E_NEEDS_CONFIRM (SQ10: asks every time), so
		// surfaces offer --yes (MayAsk).
		op.Define(op.Spec{Name: OpShow, Summary: "Show any row by id, with --origin for where it came from",
			Permission: op.PermRead, Access: op.AccessRead, Confirm: op.ConfirmRule{When: showSensitiveField, MayAsk: true},
			Errors: []errs.Code{errs.InvalidID, errs.InvalidInput, errs.NotFound, errs.InTrash, errs.Purged, errs.Unsupported, errs.RefCycle, errs.RefDepth,
				errs.RefLimit, errs.RefRequired, errs.NeedsConfirm, errs.RefScheme, errs.VaultEntryUnavailable, errs.Internal},
			Examples: []op.Example{
				{Title: "any row by its id", CLI: "lore show --id mem_01a1b49cf238767ca425d36a07b5cc1e", Want: "mem_"},
				{Title: "where it came from, as JSON", CLI: "lore show --id mem_01a1b49cf238767ca425d36a07b5cc1e --origin --json", Want: `"origin"`},
			}}, o.show),
		op.Define(op.Spec{Name: OpMove, Summary: "Move any row to another store, keeping its id (the old copy is tombstoned)",
			// MayAsk too (devco:D-762): a bulk move's secret scan refuses
			// naming the reruns with --secret-unstored, --secret-ref and
			// --not-a-secret, and the surfaces bind those flags on MayAsk
			// only; with Always alone `lore move` rejected the very
			// commands its own refusal printed.
			Permission: op.PermWrite, Access: op.AccessWrite, Confirm: op.ConfirmRule{Always: true, MayAsk: true},
			Errors: []errs.Code{errs.InvalidID, errs.InvalidInput, errs.NotFound, errs.InTrash, errs.MetaInternal, errs.MetaQuarantined, errs.LeakBlocked,
				errs.StoreReadOnly, errs.Stale, errs.Unsupported, errs.NotImplemented, errs.Internal, errs.Refused, errs.AmbiguousID}, Examples: []op.Example{{Title: "move a row to your personal store", CLI: "lore row move --id mem_0c3a --to me"},
				{Title: "move every row of a folder to another store, one commit per store (list first)", CLI: "lore move --from notes/old --to notes/new --dry-run"}}}, o.move).Asks(o.moveQuestion),
	}
}

type ops struct{ env Env }

// gone is row.show's answer for a tombstone (INTEG-TRASH-4): E_IN_TRASH
// for a trashed row, E_PURGED for a purged id, else E_NOT_FOUND. An Env
// without the lookup.Goner upgrade still refuses (E_IN_TRASH when it can
// tell, else E_NOT_FOUND): a tombstone printed as fields:{} with exit 0
// read as an existing empty row.
func (o ops) gone(ctx context.Context, ref engine.Ref) error {
	if g, ok := upgrade.As[lookup.Goner](o.env); ok {
		if err := g.Gone(ctx, ref); err != nil {
			return err
		}
	}
	return lookup.Gone(ctx, o.env, ref, errs.New(errs.NotFound, ref.ID.String()+" is deleted for good (a tombstone)"))
}

// show is row.show: the row in whatever store holds it (table from the id
// prefix), sensitive fields hidden (RQ1), --origin read straight from the
// row's envelope (PROV 9.1).
func (o ops) show(ctx context.Context, in ShowIn) (ShowOut, error) {
	id, err := ids.ParseAny(strings.TrimSpace(in.ID))
	if err != nil {
		return ShowOut{}, lookup.BadID(strings.TrimSpace(in.ID)+" is not a row id", err).
			WithHint("a row id is a kind prefix and 32 hex characters (mem_…, tsk_…, res_…); find one with lore search --query <words>")
	}
	src, r, err := o.env.Get(ctx, engine.Ref{ID: id})
	if err != nil {
		return ShowOut{}, err
	}
	if r.Deleted { // DELETION 3.2 / INTEG-TRASH-4: a deleted row is never shown as an empty live one
		return ShowOut{}, o.gone(ctx, engine.Ref{Store: src, Table: r.Table, ID: id})
	}
	out := ShowOut{ID: r.ID.String(), Table: string(r.Table), Store: string(src), Place: o.placeLabel(src), Fields: showfield.Map{},
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy}
	def, _ := o.env.TableSchema(ctx, r.Table)
	for k, v := range r.Fields {
		if f, ok := def.Field(k); ok && f.Sensitive && !in.ShowSensitive {
			out.Fields[k] = showfield.Text(hidden)
			out.MetaMasked = append(out.MetaMasked, k)
			continue
		}
		out.Fields[k] = showfield.Of(v)
	}
	if !in.ShowSensitive { // a resource kind's own sensitive fields (RQ1)
		if m, names, ok := resmask.Fields(ctx, o.env, r, hidden); ok {
			out.Fields[resmask.FieldsField] = showfield.Text(m)
			out.MetaMasked = append(out.MetaMasked, names...)
		}
	}
	labels := map[string]string{}
	for kid, j := range r.Meta {
		en, derr := meta.DecodeEntry(j)
		if derr != nil || en.Name == "" {
			continue
		}
		// METADATA 3.20 "one masker": the entry's label OR the key row's
		// flag, whichever says sensitive (an entry written before the key
		// turned sensitive carries no label)
		if !en.Sensitive {
			if _, k, kerr := o.env.Get(ctx, engine.Ref{Table: schema.TableMetaKeys, ID: kid.Any()}); kerr == nil {
				if b, ok := k.Fields["sensitive"].(value.Bool); ok && b.Bool() {
					en.Sensitive = true
				}
			}
		}
		if out.Meta == nil {
			out.Meta = map[string]string{}
		}
		switch {
		case en.Sensitive && in.ShowSensitive: // 3.20: asked first (the op confirmed)
			en.Sensitive = false
		case en.Sensitive:
			out.MetaMasked = append(out.MetaMasked, en.Name)
		}
		out.Meta[en.Name] = meta.Show(en)
		if l := metashow.Label(ctx, o.env, en); l != "" {
			labels[en.Name] = l
		}
	}
	metashow.AddLabels(out.Meta, labels) // METADATA 3.17, G-META-10
	slices.Sort(out.MetaMasked)
	o.markStale(ctx, src, r, &out)
	if in.Force && !in.Reveal {
		return ShowOut{}, errs.New(errs.InvalidInput, "--force goes with --reveal")
	}
	if in.Expand || in.Reveal {
		if err := o.expand(ctx, r, in.RevealFlag, &out); err != nil {
			return ShowOut{}, err
		}
	}
	og, oerr := o.origin(ctx, r)
	if oerr != nil {
		return ShowOut{}, oerr
	}
	out.Origin, out.Audit, out.Write, out.Lineage, out.Legacy = og.Origin, og.Audit, og.Write, og.Lineage, og.Legacy
	if in.Origin {
		out.prov = &og
	}
	out.Tags = o.tags(ctx, r)
	if out.ReferencedBy, err = o.referrers(ctx, r.ID); err != nil {
		return ShowOut{}, err
	}
	return out, nil
}

// tags names the row's tag_ids (an id whose tag row is not here keeps its
// id only).
func (o ops) tags(ctx context.Context, r row.Row) []Named {
	tids := value.ItemStrings(r.Fields[schema.FieldTagIDs])
	if len(tids) == 0 {
		return nil
	}
	out := make([]Named, 0, len(tids))
	for _, t := range tids {
		n := o.named(ctx, t, fName)
		n.URL = ""
		out = append(out, n)
	}
	return out
}

// referrers lists the live rows naming id (nil without the upgrade).
func (o ops) referrers(ctx context.Context, id ids.AnyID) ([]Referrer, error) {
	rf, ok := upgrade.As[Referrers](o.env)
	if !ok {
		return nil, nil
	}
	refs, err := rf.Referrers(ctx, id)
	if err != nil {
		return nil, err
	}
	var out []Referrer
	for _, x := range refs {
		out = append(out, Referrer{Store: string(x.Store), Table: string(x.Table), ID: x.ID.String(), Fields: x.Fields})
	}
	return out, nil
}

// expand is --expand (ops/internal/expand: the show point of refpoint;
// SECRETS 2.7, 12, 13.2) and --reveal, which implies it (expand.Reveal:
// asks every time, audited; SQ10, D18). Stored text is never changed (SQ3).
func (o ops) expand(ctx context.Context, r row.Row, rf expand.RevealFlag, out *ShowOut) error {
	ex, _ := o.env.(expand.Env)
	var b expand.Block
	var err error
	if rf.Reveal {
		b, err = expand.Reveal(ctx, ex, r, rf.Force)
	} else {
		b, err = expand.Row(ctx, ex, r)
	}
	if err != nil {
		return err
	}
	out.Expanded, out.Refs = b.Expanded, b.Refs
	out.Revealed, out.Forced, out.Warning = b.Revealed, b.Forced, b.Warning
	return nil
}

// rawOrigin is `_o` narrowed at the JSON boundary (logic/origin's keys).
type rawOrigin struct {
	Cmd        string `json:"cmd"`
	Store      string `json:"store"`
	Folder     string `json:"folder"`
	Repo       string `json:"repo"`
	Project    string `json:"project"`
	Dir        string `json:"dir"`
	Git        Git    `json:"git"`
	Import     Import `json:"import"`
	CopiedFrom string `json:"copied_from"`
	Moves      []Move `json:"moves"`
}

// rawWrite is `_w` ({v, m}, logic/origin.Stamp).
type rawWrite struct {
	V string `json:"v"`
	M string `json:"m"`
}

// origin builds the --origin part from r's envelope.
func (o ops) origin(ctx context.Context, r row.Row) (Origin, error) {
	out := Origin{Lineage: Lineage{Moves: []Move{}}}
	if !r.Origin.IsZero() {
		var ro rawOrigin
		if err := json.Unmarshal(r.Origin.Raw(), &ro); err != nil {
			return Origin{}, errs.Wrap(errs.Internal, "show: "+r.ID.String()+" has an unreadable _o", err)
		}
		out.Origin = OriginBlock{Recorded: true, Cmd: ro.Cmd, Store: o.storeNamed(ctx, ro.Store), Dir: ro.Dir, Git: ro.Git, Import: ro.Import}
		out.Origin.Git.Reason = gitReason(ro.Git, ro.Repo)
		if ro.Repo != "" {
			out.Origin.Repo = o.named(ctx, ro.Repo, fMountName, fDisplayName)
		}
		if ro.Project != "" {
			out.Origin.Project = o.named(ctx, ro.Project, fName)
		}
		if ro.Folder != "" {
			out.Origin.Folder = o.named(ctx, ro.Folder, fName)
		}
		out.Lineage.CopiedFrom = ro.CopiedFrom
		if ro.Moves != nil {
			out.Lineage.Moves = ro.Moves
		}
	}
	out.Audit = Audit{CreatedAt: stamp(r.CreatedAt), UpdatedAt: stamp(r.UpdatedAt),
		CreatedBy: o.actor(ctx, r.CreatedBy), UpdatedBy: o.actor(ctx, r.UpdatedBy)}
	if w, ok := r.Reserved[origin.KeyWrite].(value.JSON); ok && !w.IsZero() {
		var rw rawWrite
		if err := json.Unmarshal(w.Raw(), &rw); err == nil {
			out.Write = Write{Version: rw.V}
			if rw.M != "" {
				out.Write.Machine = o.named(ctx, rw.M, fLabel)
			}
		}
	}
	if out.Origin.Recorded {
		out.Origin.Version, out.Origin.Machine = out.Write.Version, out.Write.Machine
	}
	if !out.Origin.Recorded {
		sk, sr := text(r, fSourceKind), text(r, fSourceRef)
		if sk != "" || sr != "" {
			out.Legacy = Legacy{SourceKind: sk, SourceRef: sr}
		}
	}
	return out, nil
}

// storeNamed is `_o.store` with its name: a sto_ id named by the Env's
// StoreNamer; an older attached name (repo, a v0.1.17 store) is already a
// name and stays the id alone.
func (o ops) storeNamed(ctx context.Context, s string) Named {
	n := Named{ID: s}
	if s == "" {
		return Named{}
	}
	if !strings.HasPrefix(s, string(ids.PrefixStore)+"_") {
		return n // the attached name itself (repo, a v0.1.17 store)
	}
	if sn, ok := upgrade.As[StoreNamer](o.env); ok {
		if name, found := sn.StoreName(ctx, s); found {
			n.Name = name
		}
	}
	return n
}

// placeLabel is src as the other read lines name it (search, lists, the
// sql source column): "<store>/<folder>" for a store attached at a folder,
// else src. devco D-685: in an org-linked repo the org store is attached
// under the bare org name, and show's header said "policies  acme" where
// every list said acme/web.
func (o ops) placeLabel(src store.Source) string {
	d, ok := upgrade.As[store.SourceDescriber](o.env)
	if !ok || src == store.SourceRepo {
		return string(src)
	}
	ref := d.DescribeSource(src)
	if ref.Folder != "" && !strings.Contains(ref.Store, "/") {
		return ref.Store + "/" + ref.Folder
	}
	return string(src)
}

// named looks id up now and labels it with the first non-empty field
// (PROV 2: names are looked up at read time, never stored).
func (o ops) named(ctx context.Context, id string, fields ...string) Named {
	n := Named{ID: id}
	aid, err := ids.ParseAny(id)
	if err != nil {
		return n
	}
	_, r, err := o.env.Get(ctx, engine.Ref{ID: aid})
	if err != nil || r.Deleted {
		return n // not on this machine: name stays null (PROV 9.1)
	}
	for _, f := range fields {
		if s := text(r, f); s != "" {
			n.Name = s
			break
		}
	}
	n.URL = text(r, fOriginURL)
	return n
}

// actor names an audit actor (nil when the field is absent).
func (o ops) actor(ctx context.Context, id ids.ID[ids.Actor]) Actor {
	if id.IsZero() {
		return Actor{}
	}
	a := Actor{ID: id.String()}
	if _, r, err := o.env.Get(ctx, engine.Ref{Table: schema.TableActors, ID: id.Any()}); err == nil && !r.Deleted {
		a.DisplayName, a.Kind = text(r, fDisplayName), text(r, fKind)
	}
	return a
}

// text is a text field of r ("" when absent or null).
func text(r row.Row, f string) string {
	if v, ok := r.Fields[f]; ok && v != nil && v.Kind() != value.KindNull {
		return v.String()
	}
	return ""
}

// stamp formats an audit time ("" when absent).
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// Text is the human form of show (PROV 2's layout).
func (o ShowOut) Text() string {
	var b strings.Builder
	title := o.Fields["title"].String()
	if title == "" {
		title = o.Fields["name"].String()
	}
	place := o.Place
	if place == "" {
		place = o.Store
	}
	b.WriteString(o.ID + "  " + o.Table + "  " + place)
	if title != "" {
		b.WriteString("  \"" + title + "\"")
	}
	b.WriteString("\n")
	for _, k := range slices.Sorted(maps.Keys(o.Fields)) {
		v := o.Fields[k].String()
		if e, ok := o.Expanded[k]; ok {
			v = e
		}
		if v != "" {
			b.WriteString("  " + k + ": " + v + "\n")
		}
	}
	// A resource's own text values expand inside its "fields" JSON
	// (expand.Row keys them "fields.<name>"): printed under their key.
	for _, k := range slices.Sorted(maps.Keys(o.Expanded)) {
		if _, isField := o.Fields[k]; !isField {
			b.WriteString("  " + k + ": " + o.Expanded[k] + "\n")
		}
	}
	shown := metashow.Display(o.Meta, o.MetaMasked) // an allowed value by its label
	for _, k := range slices.Sorted(maps.Keys(shown)) {
		b.WriteString("  meta " + k + ": " + shown[k] + "\n")
	}
	if o.Stale != "" { // devco:D-953: the mark and its fix, as lint prints them
		b.WriteString("  " + o.Stale + "\n  fix: " + o.Reverify + "\n")
	}
	b.WriteString(auditrec.LineAt(o.CreatedAt, o.CreatedBy, o.UpdatedAt, o.UpdatedBy))
	b.WriteString(expand.RefsText(o.Refs))
	b.WriteString(expand.WarningText(o.Warning))
	if o.prov != nil {
		b.WriteString(o.prov.text())
	}
	return b.String()
}

// text is the human form of --origin.
func (g Origin) text() string {
	var b strings.Builder
	line := func(k, v string) {
		if v != "" {
			b.WriteString("  " + pad(k) + v + "\n")
		}
	}
	b.WriteString("\n")
	if !g.Origin.Recorded {
		b.WriteString("origin     not recorded (written before lore 0.1.18)\n")
		if g.Legacy != (Legacy{}) {
			b.WriteString("legacy     source_kind " + g.Legacy.SourceKind + ", source_ref " + g.Legacy.SourceRef + "  (recorded by the old fields, shown as is)\n")
		}
	} else {
		b.WriteString("origin (written once)\n")
		if st := g.Origin.Store; st.ID != "" {
			line("store", label(st))
		}
		if f := g.Origin.Folder; f.ID != "" {
			line("folder", label(f))
		}
		if p := g.Origin.Project; p.ID != "" {
			line("project", label(p))
		}
		if r := g.Origin.Repo; r.ID != "" {
			line("repo", label(r))
		}
		line("dir", g.Origin.Dir)
		if gt := g.Origin.Git; gt.State != "" {
			s := gt.State
			if gt.Branch != "" {
				s = "branch " + gt.Branch
			}
			if gt.Commit != "" {
				s += " at " + gt.Commit[:min(len(gt.Commit), shortID)]
			}
			if gt.Reason != "" {
				s += ": " + gt.Reason
			}
			line("git", s)
		}
		if g.Origin.Cmd != "" {
			line("command", "lore "+g.Origin.Cmd)
		}
		if im := g.Origin.Import; im != (Import{}) {
			line("import", strings.TrimSpace(im.Source+" "+im.ExtID))
		}
		line("lore", g.Origin.Version)
		if m := g.Origin.Machine; m.ID != "" {
			line("machine", label(Named{ID: m.ID[:min(len(m.ID), shortID)], Name: m.Name}))
		}
	}
	b.WriteString("\nwho and when\n")
	line("created", g.Audit.CreatedAt+"   "+actorText(g.Audit.CreatedBy))
	line("updated", g.Audit.UpdatedAt+"   "+actorText(g.Audit.UpdatedBy))
	if w := g.Write; w != (Write{}) && !g.Origin.Recorded {
		b.WriteString("\nlast write\n")
		line("lore", w.Version)
		if m := w.Machine; m.ID != "" {
			line("machine", label(Named{ID: m.ID[:min(len(m.ID), shortID)], Name: m.Name}))
		}
	}
	b.WriteString("\nlineage\n")
	if len(g.Lineage.Moves) == 0 {
		b.WriteString("  no moves\n")
	}
	for _, m := range g.Lineage.Moves {
		b.WriteString("  moved " + m.At + " by " + m.By + ": " + m.MovedFrom + " → " + m.MovedTo + "\n")
	}
	line("copied from", g.Lineage.CopiedFrom)
	b.WriteString("\nagent session\n  kept only on the machine that wrote the row: lore session list there\n")
	return b.String()
}

// pad aligns a label column.
func pad(k string) string {
	const width = 11
	if len(k) >= width {
		return k + " "
	}
	return k + strings.Repeat(" ", width-len(k))
}

// label is "name  id  (url)" with the parts present.
func label(n Named) string {
	s := n.ID
	if n.Name != "" {
		s = n.Name + "  " + n.ID
	}
	if n.URL != "" {
		s += "  (" + n.URL + ")"
	}
	return s
}

// actorText is "display name (id)".
func actorText(a Actor) string {
	if a.ID == "" {
		return "unknown"
	}
	if a.DisplayName == "" {
		return a.ID
	}
	return a.DisplayName + " (" + a.ID + ")"
}

// move is row.move: engine.MoveWith keeps the id for every noun (PROV5,
// XQ5); --drop-internal and --with-time pass through (MQ11, TQ3).
func (o ops) move(ctx context.Context, in MoveIn) (MoveOut, error) {
	if bulk, err := in.bulk(); err != nil || bulk {
		if err != nil {
			return MoveOut{}, err
		}
		return o.moveMany(ctx, in)
	}
	id, err := ids.ParseAny(strings.TrimSpace(in.ID))
	if err != nil {
		return MoveOut{}, lookup.BadID(strings.TrimSpace(in.ID)+" is not a row id", err)
	}
	to := strings.TrimSpace(in.To)
	if to == "" {
		return MoveOut{}, errs.New(errs.InvalidInput, "move: --to is required (a store name from .lore/stores.yaml, or repo)")
	}
	wt := engine.TimeShare(strings.TrimSpace(in.WithTime))
	if wt != "" && !slices.Contains(engine.TimeShares(), string(wt)) {
		return MoveOut{}, errs.New(errs.InvalidInput, "move: --with-time takes "+strings.Join(engine.TimeShares(), " or "))
	}
	mv, ok := o.env.(Mover)
	if !ok {
		return MoveOut{}, errs.New(errs.Unsupported, "move: this build's store wiring cannot move rows")
	}
	// Moving to the store that already holds the row: say so, as memory
	// move does, instead of the engine's "no row … outside <store>" or its
	// E_NOT_FOUND for a move run twice (nothing is missing). A trashed row
	// is E_IN_TRASH wherever --to points (DELETION 3.2), never the
	// engine's "no row … outside <store>" when it sits in that store.
	if src, r, gerr := o.env.Get(ctx, engine.Ref{ID: id}); gerr == nil {
		if r.Deleted {
			if terr := lookup.InTrash(ctx, o.env, engine.Ref{Table: r.Table, ID: id}); terr != nil {
				return MoveOut{}, terr
			}
		} else if src == store.Source(to) {
			return MoveOut{}, errs.New(errs.InvalidInput, "move: "+id.String()+" is already in store "+to+"; nothing to move").
				WithHint("name another attached store with --to (`lore store list` shows them); lore row show --id " + id.String() + " --origin shows where it lives")
		}
	}
	// moving a company must rule / critical or high hotfix out of its
	// place takes it from every repo of the org: an admin's change, as
	// its delete is (devco D-659)
	// the full place (a linked folder arrives as the bare org name, D-697)
	// and the --setup given, so the admin's line moves this row anywhere
	place := profilestore.PlaceTo(ctx, o.env, store.Source(to))
	line := "lore row move --id " + id.String() + " --to " + place + setupFlag(ctx)
	var dups []opsstores.NearDuplicate
	if src, r, gerr := o.env.Get(ctx, engine.Ref{ID: id}); gerr == nil && !r.Deleted {
		if err = profilestore.RequireAdminForRows(ctx, o.env, []profilestore.Placed{{Source: src, Row: r}},
			"move", line, ""); err != nil {
			return MoveOut{}, err
		}
		// D-854: the bulk move's near-duplicate check, read before the
		// move (after it the row is in --to, one copy)
		if dups, err = o.oneNearDuplicates(ctx, r, store.Source(to), in.WithChildren); err != nil {
			return MoveOut{}, err
		}
	}
	ctx = moveAsk(ctx, "move "+id.String()+" to "+place, line+" --yes")
	w, err := mv.MoveWith(ctx, engine.Ref{ID: id}, store.Source(to), engine.MoveOptions{DropInternal: in.DropInternal, WithTime: wt, WithChildren: in.WithChildren})
	if err != nil {
		return MoveOut{}, err
	}
	out := MoveOut{ID: id.String(), Table: string(w.Row.Table), To: to, Duplicates: dups, place: place, setup: setupFlag(ctx)}
	for _, c := range w.Also { // --with-children: the sub-tasks moved with it
		out.Children = append(out.Children, c.Row.ID.String())
	}
	for _, wn := range w.Warnings {
		if noteCode(wn.Code) { // TQ3 lines (design 8), D-891 tag copies
			out.Notes = append(out.Notes, wn.Message)
		} else {
			out.Warnings = append(out.Warnings, wn.Message)
		}
	}
	return out, nil
}
