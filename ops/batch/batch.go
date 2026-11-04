// Package batch defines the `batch` noun's read operations (layer L5,
// CLI_REFERENCE 3.10): `lore batch list` and `lore batch show`.
//
// XQ1: every bulk action of every noun writes under one bat_ id with a kind
// (task, meta, delete, resource, import, secrets), and that id is carried on
// the audit event of each row it wrote; a batch is never a row. So these
// operations only read the batch log (Env.BatchEvents). In 0.1.18 that log
// is machine-local (Q-R3-8: a batch is visible on the machine that ran it),
// and `lore batch undo` is deferred (Q-R3-1).
package batch

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (`lore batch …`, REST /v1/batches).
const (
	Noun   = "batch"
	OpList = "batch.list"
	OpShow = "batch.show"
)

// Output words.
const (
	// sampleRows is how many rows `batch show` lists without --all (the
	// CLI_REFERENCE sample shows a few ids, then the --all hint).
	sampleRows = 10
	timeLayout = "2006-01-02 15:04"
	dateLayout = "2006-01-02"
	handleSign = "@"
	fieldHand  = "handle"
	fieldName  = "display_name"
	fieldKey   = "stable_key"
	hoursInDay = 24
	daySuffix  = "d"
)

// Kind is a batch's kind (bulk.Kind, re-exported so the composition root,
// which cannot import ops/internal, can type the log it reads).
type Kind = bulk.Kind

// Event is one audit event of a batch, as the batch log keeps it: names
// and ids only, never a field value (engine.Event's rule).
type Event struct {
	Batch  ids.ID[ids.Batch]
	Kind   Kind
	Op     string // the operation that wrote it ("resource.delete")
	Action string // create, update, delete, purge
	Store  store.Source
	Table  row.Table
	ID     ids.AnyID
	Actor  ids.ID[ids.Actor]
	At     time.Time
}

// Env is what the batch operations read; *wire.App implements it.
type Env interface {
	// BatchEvents returns every event of every batch this machine
	// recorded, in the order they were written.
	BatchEvents(ctx context.Context) ([]Event, error)
}

// Actors is the optional Env upgrade `--by` uses to find an actor by
// handle, display name or stable key (wire.App has it). Without it --by
// takes an act_ id only.
type Actors interface {
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	Stores() []store.Source
}

// ListIn is the input of batch.list.
type ListIn struct {
	// By is always an actor (XQ8): an act_ id or @handle.
	By    op.ActorRef `json:"by,omitempty" flag:"by" desc:"only batches by this actor (@handle, stable key or act_ id)"`
	Since string      `json:"since,omitempty" flag:"since" desc:"only batches since a date (YYYY-MM-DD) or a duration (36h, 7d)"`
	Kind  string      `json:"kind,omitempty" flag:"kind" desc:"only batches of this kind: task | meta | delete | resource | import | secrets"`
}

// ShowIn is the input of batch.show.
type ShowIn struct {
	ID  string `json:"id" arg:"0" desc:"the batch id (bat_…)"`
	All bool   `json:"all,omitempty" flag:"all" desc:"list every row instead of a sample"`
}

// Summary is one batch as listed.
type Summary struct {
	ID      string  `json:"id"`
	Kind    string  `json:"kind"`
	Op      string  `json:"op"`
	Actor   string  `json:"actor"`
	At      string  `json:"at"`
	Rows    int     `json:"rows"`
	Stores  []Count `json:"stores"`
	Actions []Count `json:"actions"`
	// stores and actions are the tallies Stores and Actions are built from.
	stores, actions map[string]int
}

// Count is how many rows of a batch one store (or one action) holds.
type Count struct {
	Name string `json:"name"`
	Rows int    `json:"rows"`
}

// RowRef is one row a batch wrote.
type RowRef struct {
	ID     string `json:"id"`
	Table  string `json:"table"`
	Store  string `json:"store"`
	Action string `json:"action"`
}

// ListOut is the output of batch.list.
type ListOut struct {
	Data  []Summary `json:"data"`
	Count int       `json:"count"`
}

// ShowOut is the output of batch.show.
type ShowOut struct {
	Data    Summary  `json:"data"`
	RowList []RowRef `json:"row_list"`
	// Shown is how many of RowList the human text printed.
	Shown int `json:"-"`
}

// Ops returns the batch operations bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	errsRead := []errs.Code{errs.InvalidInput, errs.InvalidID, errs.NotFound, errs.Internal}
	return []op.Entry{
		op.Define(op.Spec{Name: OpList, Summary: "List bulk actions (batches) run on this machine",
			Permission: op.PermRead, Access: op.AccessRead, Errors: errsRead,
			Examples: []op.Example{{Title: "import batches as JSON", CLI: "lore batch list --kind import --json", Want: `"data"`}}}, o.list),
		op.Define(op.Spec{Name: OpShow, Summary: "Show one batch: its kind, who ran it and the rows it wrote",
			Permission: op.PermRead, Access: op.AccessRead, Errors: errsRead}, o.show),
	}
}

// ops binds the run functions to one Env.
type ops struct{ env Env }

func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	var kind bulk.Kind
	if in.Kind != "" {
		kind = bulk.Kind(in.Kind)
		if !kind.Valid() {
			return ListOut{}, errs.New(errs.InvalidInput, "batch list: --kind "+strconv.Quote(in.Kind)+" is not one of "+kindNames())
		}
	}
	since, err := sinceOf(in.Since, time.Now())
	if err != nil {
		return ListOut{}, err
	}
	by, err := o.actorOf(ctx, string(in.By))
	if err != nil {
		return ListOut{}, err
	}
	evs, err := o.env.BatchEvents(ctx)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{Data: []Summary{}}
	for _, s := range summarize(evs) {
		if kind != "" && s.Kind != string(kind) || by != "" && s.Actor != by || !since.IsZero() && s.at.Before(since) {
			continue
		}
		out.Data = append(out.Data, s.Summary)
	}
	out.Count = len(out.Data)
	return out, nil
}

func (o ops) show(ctx context.Context, in ShowIn) (ShowOut, error) {
	id, err := ids.Parse[ids.Batch](strings.TrimSpace(in.ID))
	if err != nil {
		return ShowOut{}, err
	}
	evs, err := o.env.BatchEvents(ctx)
	if err != nil {
		return ShowOut{}, err
	}
	var mine []Event
	for _, ev := range evs {
		if ev.Batch == id {
			mine = append(mine, ev)
		}
	}
	if len(mine) == 0 {
		return ShowOut{}, errs.New(errs.NotFound, "batch "+id.String()+" is not in this machine's batch log").
			WithHint("lore batch list shows the batches run on this machine")
	}
	sums := summarize(mine)
	out := ShowOut{Data: sums[0].Summary, RowList: make([]RowRef, 0, len(mine))}
	for _, ev := range mine {
		out.RowList = append(out.RowList, RowRef{ID: ev.ID.String(), Table: ev.Table.String(), Store: string(ev.Store), Action: ev.Action})
	}
	out.Shown = len(out.RowList)
	if !in.All && out.Shown > sampleRows {
		out.Shown = sampleRows
	}
	return out, nil
}

// summary is a Summary plus its time for filtering.
type summary struct {
	Summary
	at time.Time
}

// summarize groups events by batch, newest batch first. A batch's kind,
// op and actor are its first event's (one bulk action has one of each).
func summarize(evs []Event) []summary {
	by := map[ids.ID[ids.Batch]]*summary{}
	var order []ids.ID[ids.Batch]
	for _, ev := range evs {
		s, ok := by[ev.Batch]
		if !ok {
			s = &summary{Summary: Summary{ID: ev.Batch.String(), Kind: string(ev.Kind), Op: ev.Op, Actor: ev.Actor.String(),
				At: ev.At.Local().Format(timeLayout), stores: map[string]int{}, actions: map[string]int{}}, at: ev.At}
			by[ev.Batch] = s
			order = append(order, ev.Batch)
		}
		s.Rows++
		s.stores[string(ev.Store)]++
		s.actions[ev.Action]++
	}
	out := make([]summary, 0, len(order))
	for _, id := range order {
		s := by[id]
		s.Stores, s.Actions = tally(s.stores), tally(s.actions)
		out = append(out, *s)
	}
	slices.SortStableFunc(out, func(a, b summary) int { return b.at.Compare(a.at) })
	return out
}

// sinceOf parses --since: a date, a Go duration or a day count ("7d").
func sinceOf(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.ParseInLocation(dateLayout, s, time.Local); err == nil {
		return t, nil
	}
	if n, ok := strings.CutSuffix(s, daySuffix); ok {
		if d, err := strconv.Atoi(n); err == nil && d >= 0 {
			return now.Add(-time.Duration(d) * hoursInDay * time.Hour), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, errs.New(errs.InvalidInput, "batch list: --since "+strconv.Quote(s)+": want a date (YYYY-MM-DD) or a duration (36h, 7d)")
}

// actorOf resolves --by to an act_ id string ("" = no filter).
func (o ops) actorOf(ctx context.Context, by string) (string, error) {
	by = strings.TrimSpace(by)
	if by == "" {
		return "", nil
	}
	if id, err := ids.Parse[ids.Actor](by); err == nil {
		return id.String(), nil
	}
	name := strings.TrimPrefix(by, handleSign)
	a, ok := upgrade.As[Actors](o.env)
	if !ok {
		return "", errs.New(errs.InvalidInput, "batch list: --by "+by+": this surface resolves no handles or stable keys; pass the act_ id")
	}
	for _, src := range a.Stores() {
		rows, err := a.List(ctx, src, schema.TableActors)
		if err != nil {
			return "", err
		}
		for _, r := range rows {
			if r.Deleted {
				continue
			}
			if text(r, fieldHand) == name || text(r, fieldName) == name || text(r, fieldKey) == by {
				return r.ID.String(), nil
			}
		}
	}
	return "", errs.New(errs.NotFound, "batch list: no actor "+by+" in the attached stores").WithHint("lore actor list")
}

// text reads a text field ("" when absent).
func text(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// kindNames lists the kinds for a message.
func kindNames() string {
	ks := bulk.Kinds()
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = string(k)
	}
	return strings.Join(out, ", ")
}

// Text implements op.Texter.
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "no batches on this machine\n"
	}
	var b strings.Builder
	for _, s := range o.Data {
		b.WriteString(s.line())
	}
	return b.String()
}

// line is the CLI_REFERENCE 3.10 header line of a batch.
func (s Summary) line() string {
	return "batch " + s.ID + "  " + s.Kind + "  " + strings.ReplaceAll(s.Op, ".", " ") + "  by " + s.Actor + "  " + s.At + "  " +
		strconv.Itoa(s.Rows) + " rows (" + counts(s.Stores) + ")\n"
}

// tally turns counts by name into a list in name order.
func tally(m map[string]int) []Count {
	out := make([]Count, 0, len(m))
	for k, n := range m {
		out = append(out, Count{Name: k, Rows: n})
	}
	slices.SortFunc(out, func(a, b Count) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// counts renders "agency 5, repo 9".
func counts(cs []Count) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = c.Name + " " + strconv.Itoa(c.Rows)
	}
	return strings.Join(parts, ", ")
}

// Text implements op.Texter.
func (o ShowOut) Text() string {
	var b strings.Builder
	b.WriteString(o.Data.line())
	b.WriteString("  kind: " + o.Data.Kind + "; " + counts(o.Data.Actions) + "\n")
	for _, r := range o.RowList[:o.Shown] {
		b.WriteString("  " + r.Action + " " + r.ID + " (" + r.Table + ", " + r.Store + ")\n")
	}
	if rest := len(o.RowList) - o.Shown; rest > 0 {
		b.WriteString("  … " + strconv.Itoa(rest) + " more (lore batch show " + o.Data.ID + " --all)\n")
	}
	return b.String()
}
