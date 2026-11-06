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
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/metashow"
	"github.com/thesatellite-ai/lore/ops/internal/resmask"
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
	ID     string `json:"id" arg:"0" desc:"any row id (mem_…, tsk_…, res_…, var_…)"`
	Origin bool   `json:"origin,omitempty" flag:"origin" desc:"also print where the row came from: _o, the audit fields, the _w stamp and its moves (PROV 9.1)"`
	// ShowSensitive: META 3.20 / RQ1 (asks first; --yes without a
	// terminal, XQ3; agents may run it too, OQ25).
	ShowSensitive bool `json:"show_sensitive,omitempty" flag:"show-sensitive" desc:"print sensitive field and metadata values (asks first)"`
	expand.Flag
	expand.RevealFlag
}

// MoveIn is the input of row.move.
type MoveIn struct {
	ID           string `json:"id" arg:"0" desc:"any row id"`
	To           string `json:"to" flag:"to" desc:"the target store: repo | me | <store>[/<folder>]"`
	DropInternal bool   `json:"drop_internal,omitempty" flag:"drop-internal" desc:"into a client repo: remove the row's internal metadata values instead of refusing (MQ11, BATCH 7)"`
	WithTime     string `json:"with_time,omitempty" flag:"with-time" desc:"totals | none: per-worker time totals for a task moved into a client repo (TQ3; default: the time.client_share policy, none)"`
	WithChildren bool   `json:"with_children,omitempty" flag:"with-children" desc:"a task: move its whole subtree too; every sub-task keeps its id and parent (TASK 6.14, B7)"`
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

// Git is `_o.git`.
type Git struct {
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	State  string `json:"state"`
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
// written before 0.1.18 (never guessed, PROV 9.1).
type OriginBlock struct {
	Recorded bool   `json:"recorded"`
	Cmd      string `json:"cmd,omitempty"`
	Store    string `json:"store,omitempty"`
	Folder   Named  `json:"folder,omitzero"`
	Repo     Named  `json:"repo,omitzero"`
	Project  Named  `json:"project,omitzero"`
	Dir      string `json:"dir,omitempty"`
	Git      Git    `json:"git,omitzero"`
	Import   Import `json:"import,omitzero"`
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
	ID     string            `json:"id"`
	Table  string            `json:"table"`
	Store  string            `json:"store"`
	Fields map[string]string `json:"fields"`
	Meta   map[string]string `json:"meta,omitempty"`
	// MetaMasked names the metadata keys and the sensitive fields printed
	// masked (META 3.14/3.20 meta_masked); empty with --show-sensitive.
	MetaMasked []string `json:"meta_masked,omitempty"`
	// The --origin blocks (PROV 9.1's JSON keys), nil without --origin.
	// (The session block lives only on the writing machine's sessions
	// table, AID 6.6, so show never carries it.)
	Origin  OriginBlock `json:"origin,omitzero"`
	Audit   Audit       `json:"audit,omitzero"`
	Write   Write       `json:"write,omitzero"`
	Lineage Lineage     `json:"lineage,omitzero"`
	Legacy  Legacy      `json:"legacy,omitzero"`
	// Expanded is each text field that --expand changed, filled in
	// (SECRETS 12: "--json adds expanded and refs"); Refs lists every
	// reference met, with its value or why it stayed as written.
	Expanded map[string]string `json:"expanded,omitempty"`
	Refs     []refpoint.Used   `json:"refs,omitempty"`
	// Revealed, Forced and Warning: --reveal (SECRETS 2.7, SQ10).
	Revealed int    `json:"revealed,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	Warning  string `json:"warning,omitempty"`
	prov     *Origin
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
}

// Text is the human form of a move.
func (o MoveOut) Text() string {
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
			Errors: []errs.Code{errs.InvalidID, errs.InvalidInput, errs.NotFound, errs.Unsupported, errs.RefCycle, errs.RefDepth,
				errs.RefLimit, errs.RefRequired, errs.NeedsConfirm, errs.RefScheme, errs.VaultEntryUnavailable, errs.Internal}}, o.show),
		op.Define(op.Spec{Name: OpMove, Summary: "Move any row to another store, keeping its id (the old copy is tombstoned)",
			Permission: op.PermWrite, Access: op.AccessWrite, Confirm: op.ConfirmRule{Always: true},
			Errors: []errs.Code{errs.InvalidID, errs.InvalidInput, errs.NotFound, errs.MetaInternal, errs.MetaQuarantined, errs.LeakBlocked,
				errs.StoreReadOnly, errs.Stale, errs.Unsupported, errs.NotImplemented, errs.Internal}}, o.move).Asks(o.moveQuestion),
	}
}

type ops struct{ env Env }

// show is row.show: the row in whatever store holds it (table from the id
// prefix), sensitive fields hidden (RQ1), --origin read straight from the
// row's envelope (PROV 9.1).
func (o ops) show(ctx context.Context, in ShowIn) (ShowOut, error) {
	id, err := ids.ParseAny(strings.TrimSpace(in.ID))
	if err != nil {
		return ShowOut{}, lookup.BadID(strings.TrimSpace(in.ID)+" is not a row id", err)
	}
	src, r, err := o.env.Get(ctx, engine.Ref{ID: id})
	if err != nil {
		return ShowOut{}, err
	}
	if r.Deleted { // DELETION 3.2: a trashed row says where it is; a tombstone shows as it is
		if terr := lookup.InTrash(ctx, o.env, engine.Ref{Store: src, Table: r.Table, ID: id}); terr != nil {
			return ShowOut{}, terr
		}
	}
	out := ShowOut{ID: r.ID.String(), Table: string(r.Table), Store: string(src), Fields: map[string]string{}}
	def, _ := o.env.TableSchema(ctx, r.Table)
	for k, v := range r.Fields {
		if f, ok := def.Field(k); ok && f.Sensitive && !in.ShowSensitive {
			out.Fields[k] = hidden
			out.MetaMasked = append(out.MetaMasked, k)
			continue
		}
		out.Fields[k] = v.String()
	}
	if !in.ShowSensitive { // a resource kind's own sensitive fields (RQ1)
		if m, names, ok := resmask.Fields(ctx, o.env, r, hidden); ok {
			out.Fields[resmask.FieldsField] = m
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
	if in.Force && !in.Reveal {
		return ShowOut{}, errs.New(errs.InvalidInput, "--force goes with --reveal")
	}
	if in.Expand || in.Reveal {
		if err := o.expand(ctx, r, in.RevealFlag, &out); err != nil {
			return ShowOut{}, err
		}
	}
	if in.Origin {
		og, oerr := o.origin(ctx, r)
		if oerr != nil {
			return ShowOut{}, oerr
		}
		out.Origin, out.Audit, out.Write, out.Lineage, out.Legacy, out.prov = og.Origin, og.Audit, og.Write, og.Lineage, og.Legacy, &og
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
		out.Origin = OriginBlock{Recorded: true, Cmd: ro.Cmd, Store: ro.Store, Dir: ro.Dir, Git: ro.Git, Import: ro.Import}
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
	if !out.Origin.Recorded {
		sk, sr := text(r, fSourceKind), text(r, fSourceRef)
		if sk != "" || sr != "" {
			out.Legacy = Legacy{SourceKind: sk, SourceRef: sr}
		}
	}
	return out, nil
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
	title := o.Fields["title"]
	if title == "" {
		title = o.Fields["name"]
	}
	b.WriteString(o.ID + "  " + o.Table + "  " + o.Store)
	if title != "" {
		b.WriteString("  \"" + title + "\"")
	}
	b.WriteString("\n")
	for _, k := range slices.Sorted(maps.Keys(o.Fields)) {
		v := o.Fields[k]
		if e, ok := o.Expanded[k]; ok {
			v = e
		}
		if v != "" {
			b.WriteString("  " + k + ": " + v + "\n")
		}
	}
	shown := metashow.Display(o.Meta, o.MetaMasked) // an allowed value by its label
	for _, k := range slices.Sorted(maps.Keys(shown)) {
		b.WriteString("  meta " + k + ": " + shown[k] + "\n")
	}
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
		line("store", g.Origin.Store)
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
			line("git", s)
		}
		if g.Origin.Cmd != "" {
			line("command", "lore "+g.Origin.Cmd)
		}
		if im := g.Origin.Import; im != (Import{}) {
			line("import", strings.TrimSpace(im.Source+" "+im.ExtID))
		}
	}
	b.WriteString("\nwho and when\n")
	line("created", g.Audit.CreatedAt+"   "+actorText(g.Audit.CreatedBy))
	line("updated", g.Audit.UpdatedAt+"   "+actorText(g.Audit.UpdatedBy))
	if w := g.Write; w != (Write{}) {
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
	w, err := mv.MoveWith(ctx, engine.Ref{ID: id}, store.Source(to), engine.MoveOptions{DropInternal: in.DropInternal, WithTime: wt, WithChildren: in.WithChildren})
	if err != nil {
		return MoveOut{}, err
	}
	out := MoveOut{ID: id.String(), Table: string(w.Row.Table), To: to}
	for _, c := range w.Also { // --with-children: the sub-tasks moved with it
		out.Children = append(out.Children, c.Row.ID.String())
	}
	for _, wn := range w.Warnings {
		switch wn.Code {
		case engine.WarnTimeKeptBack, engine.WarnInternalKeptBack, engine.WarnTimeTotals:
			out.Notes = append(out.Notes, wn.Message) // TQ3 lines (design 8)
		default:
			out.Warnings = append(out.Warnings, wn.Message)
		}
	}
	return out, nil
}
