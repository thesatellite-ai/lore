// Package wheredel is the bulk `<noun> delete --where` of the nouns that
// have no richer filter of their own (DELETION 3.2, 2.8; FA-LTASK-2):
// tasklist, mission, plan, memory, rule. The filter is the one --where
// grammar (logic/filter, XQ7) over the table's own visible fields plus the
// envelope keys id, created_at, updated_at. Tasks keep their own form
// (ops/task deleteOp: people keys, SLA, meta).
//
//	Run ─► Parse + Bind (unknown key E_INVALID_INPUT with the key list;
//	        meta.<key> refused: no metadata filter here)
//	    ─► match every live row of every attached store
//	    ─► PreviewDelete over all matches (dependents counted, refusals
//	        such as E_HAS_DEPENDENTS before any write)
//	    ─► --dry-run: list, write nothing
//	    ─► bulk.Ask (always: a filter can name many rows, TSQ3 as task
//	        delete --where) ─► bulk.Begin(delete) ─► Fence (the caller's
//	        claim fence over the tasks the cascade takes, FA-TASK-7)
//	    ─► Delete per match, one bat_ id; a partial failure names the batch
package wheredel

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/filter"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Envelope keys every table filters on beside its own fields.
const (
	keyID        = "id"
	keyCreatedAt = "created_at"
	keyUpdatedAt = "updated_at"
)

// listedInQuestion is how many matches the question and the dry run name
// one by one before "and N more" (the rest are counted).
const listedInQuestion = 20

// Env is what Run needs of the engine: the attached stores, their rows and
// the delete. ops/memory.Env (and so work.Env and knowledge.Env) has it.
type Env interface {
	Stores() []store.Source
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	Delete(ctx context.Context, ref engine.Ref) (engine.Written, error)
}

// Previewer is the optional Env upgrade that plans a delete without
// writing (engine.PreviewDelete; wire.App has it). Without it the question
// counts only the matches and nothing is fenced.
type Previewer interface {
	PreviewDelete(ctx context.Context, refs ...engine.Ref) (engine.DeletePreview, error)
}

// Located is one row with the store that holds it (the same shape as
// work.Sourced, so a caller converts with work.Sourced(l)).
type Located struct {
	Source store.Source
	Row    row.Row
}

// Spec names the noun Run deletes from.
type Spec struct {
	// Noun is the CLI noun ("tasklist") for messages.
	Noun string
	// Table is the schema table.
	Table row.Table
	// Fence, when set, is called inside the delete batch before the first
	// tombstone with the tasks the whole delete takes (preview rows); it
	// returns how many claimed tasks it fenced (FA-TASK-7).
	Fence func(ctx context.Context, tasks []Located) (int, error)
	// Aliases renames columns for --where: user key → stored column
	// (tasklist: "status" → "status_str", the name its flags and help
	// use). The column's own name still filters (a script written
	// against it keeps working) but is no longer listed, so the refusal
	// naming the fields shows only what a person types.
	Aliases map[string]string
}

// Result is what a bulk delete did (or, with DryRun, would do).
type Result struct {
	// Batch is the bat_ id ("" for a dry run or no match).
	Batch string
	// IDs are the matched rows, in store then id order.
	IDs []string
	// Human is the text output.
	Human string
	// Rows are the matched rows, each with the store it is in (the same
	// order as IDs), so a bulk answer names where it wrote like a
	// single-row one.
	Rows []Row
	// Store is the store every matched row is in; "" when they are in
	// several (each Row then names its own) or nothing matched.
	Store string
}

// Row is one row a bulk write matched and the store it is in.
type Row struct {
	ID    string `json:"id"`
	Store string `json:"store"`
}

// Run deletes every live row of sp.Table matching where as one batch (see
// the package comment for the flow).
func Run(ctx context.Context, env Env, sp Spec, where []string, dryRun bool) (Result, error) {
	m, keys, err := matcher(sp, where)
	cols := columns(keys, sp.Aliases)
	if err != nil {
		return Result{}, err
	}
	var hits []Located
	for _, src := range engine.ReadStoresOf(env) {
		rows, lerr := env.List(ctx, src, sp.Table)
		if lerr != nil {
			return Result{}, lerr
		}
		slices.SortFunc(rows, func(a, b row.Row) int { return strings.Compare(a.ID.String(), b.ID.String()) })
		for _, r := range rows {
			if !r.Deleted && m.Match(getter(r, cols)) {
				hits = append(hits, Located{Source: src, Row: r})
			}
		}
	}
	out := Result{IDs: make([]string, 0, len(hits))}
	if len(hits) == 0 {
		out.Human = "no " + sp.Noun + " matches --where; nothing deleted\n"
		return out, nil
	}
	refs := make([]engine.Ref, 0, len(hits))
	for i, h := range hits {
		out.IDs = append(out.IDs, h.Row.ID.String())
		out.Rows = append(out.Rows, Row{ID: h.Row.ID.String(), Store: string(h.Source)})
		switch {
		case i == 0:
			out.Store = string(h.Source)
		case out.Store != string(h.Source):
			out.Store = ""
		}
		refs = append(refs, engine.Ref{Store: h.Source, Table: sp.Table, ID: h.Row.ID})
	}
	var tasks []Located
	deps := 0
	if pv, ok := upgrade.As[Previewer](env); ok {
		p, perr := pv.PreviewDelete(ctx, refs...)
		if perr != nil {
			return Result{}, perr
		}
		deps = p.Dependents(len(hits))
		for _, c := range p.Rows {
			if c.Row.Table == schema.TableTasks {
				tasks = append(tasks, Located{Source: c.Store, Row: c.Row})
			}
		}
	}
	if dryRun {
		out.Human = "would delete " + describe(sp.Noun, hits, deps, len(tasks), true) + " (nothing written, --dry-run)\n"
		return out, nil
	}
	// The question travels in E_NEEDS_CONFIRM's message (logs, agents,
	// REST bodies), so it names ids only, never a title or body: the same
	// rule as the engine's generic question (G-ENGINE-5).
	if err = bulk.Ask(ctx, "delete "+describe(sp.Noun, hits, deps, len(tasks), false)+" move all of them to the trash?"); err != nil {
		return Result{}, err
	}
	bctx, batch, err := bulk.Begin(ctx, bulk.KindDelete)
	if err != nil {
		return Result{}, err
	}
	out.Batch = batch.String()
	fenced := 0
	if sp.Fence != nil {
		if fenced, err = sp.Fence(bctx, tasks); err != nil {
			return Result{}, err
		}
	}
	for i, ref := range refs {
		if _, derr := env.Delete(bctx, ref); derr != nil {
			if i > 0 {
				return Result{}, errs.Prefix(sp.Noun+" delete --where: "+ref.ID.String()+" failed after "+strconv.Itoa(i)+
					" of "+strconv.Itoa(len(refs))+" went to the trash in batch "+out.Batch+" (lore trash restore "+out.Batch+")", derr)
			}
			return Result{}, derr
		}
	}
	label := strconv.Itoa(len(hits)) + " " + sp.Noun + "(s) (--where)"
	if deps > 0 {
		label += " + " + strconv.Itoa(deps) + " rows"
	}
	out.Human = trashline.MovedBatch(label, out.Batch)
	if fenced > 0 {
		out.Human += "  fenced " + strconv.Itoa(fenced) + " claimed task(s): their holders' next write fails\n"
	}
	return out, nil
}

// describe is the question / dry-run text: the count, the dependents and
// the first matches by id, and with labels (the dry run's output only,
// never the question) their title or body start.
func describe(noun string, hits []Located, deps, tasks int, labels bool) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(len(hits)) + " " + noun + "(s)")
	if deps > 0 {
		b.WriteString(" and " + strconv.Itoa(deps) + " row(s) that go with them (" + strconv.Itoa(tasks) + " task(s))")
	}
	b.WriteString(":")
	for i, h := range hits {
		if i == listedInQuestion {
			b.WriteString(" and " + strconv.Itoa(len(hits)-listedInQuestion) + " more")
			break
		}
		b.WriteString(" " + h.Row.ID.String())
		if t := label(h.Row); labels && t != "" {
			b.WriteString(" " + t)
		}
		b.WriteString(";")
	}
	return b.String()
}

// matcher parses and binds where against the table's keys.
func matcher(sp Spec, where []string) (filter.Matcher, map[string]filter.Field, error) {
	keys := Keys(sp.Table)
	for user, col := range sp.Aliases {
		if f, ok := keys[col]; ok {
			keys[user] = f
			delete(keys, col)
		}
	}
	f, err := filter.Parse(where)
	if err != nil {
		return filter.Matcher{}, nil, err
	}
	if f.Empty() {
		return filter.Matcher{}, nil, errs.New(errs.InvalidInput, sp.Noun+" delete --where: the filter is empty")
	}
	m, err := f.Bind(func(k filter.Key) (filter.Field, error) {
		if k.Meta {
			return filter.Field{}, errs.New(errs.InvalidInput, sp.Noun+" delete --where: "+k.String()+": metadata terms are not taken here").
				WithHint("filter on a " + sp.Noun + " field (" + strings.Join(sortedKeys(keys), ", ") + ")")
		}
		fd, ok := keys[k.Name]
		if !ok {
			fd, ok = keys[aliasOf(sp.Aliases, k.Name)]
		}
		if !ok {
			return filter.Field{}, filter.UnknownKey(k, sortedKeys(keys))
		}
		return fd, nil
	})
	return m, keys, err
}

// Keys are the --where keys of table: every visible field (internal,
// sensitive and JSON fields left out, as task list does) plus the
// envelope keys. Built from the schema, so a new field filters at once.
func Keys(table row.Table) map[string]filter.Field {
	out := map[string]filter.Field{
		keyID:        {Kind: value.KindText},
		keyCreatedAt: {Kind: value.KindDateTime},
		keyUpdatedAt: {Kind: value.KindDateTime},
	}
	for _, t := range schema.Builtin() {
		if t.Name != table {
			continue
		}
		for _, f := range t.Fields {
			if f.Internal || f.Sensitive || f.Kind == value.KindJSON {
				continue
			}
			out[f.Name] = filter.Field{Kind: f.Kind, Elem: f.Elem, Values: f.Values}
		}
	}
	return out
}

// aliasOf is the user key whose alias targets column ("" when none).
func aliasOf(aliases map[string]string, column string) string {
	for user, col := range aliases {
		if col == column {
			return user
		}
	}
	return ""
}

// column is one --where key bound to the stored field it reads.
type column struct {
	name  string // the stored field (the alias's target, else the key)
	field filter.Field
}

// columns binds every key to its stored field through aliases.
func columns(keys map[string]filter.Field, aliases map[string]string) map[string]column {
	out := make(map[string]column, len(keys))
	for k, f := range keys {
		name := k
		if col, ok := aliases[k]; ok {
			name = col
		}
		out[k] = column{name: name, field: f}
		if name != k {
			out[name] = column{name: name, field: f} // the unlisted column name
		}
	}
	return out
}

// getter reads a row's values for a key.
func getter(r row.Row, cols map[string]column) filter.Getter {
	return func(k filter.Key) []string {
		c := cols[k.Name]
		switch k.Name {
		case keyID:
			return []string{r.ID.String()}
		case keyCreatedAt:
			return timeText(r.CreatedAt)
		case keyUpdatedAt:
			return timeText(r.UpdatedAt)
		}
		if c.field.Kind == value.KindList {
			return value.ItemStrings(r.Fields[c.name])
		}
		if s := text(r, c.name); s != "" {
			return []string{s}
		}
		return nil
	}
}

// labelRunes caps a body shown as a row's label in the question.
const labelRunes = 40

// label is a row's title, else the start of its body (memories and rules
// have no title).
func label(r row.Row) string {
	if t := text(r, "title"); t != "" {
		return t
	}
	b := []rune(strings.Join(strings.Fields(text(r, "body")), " "))
	if len(b) > labelRunes {
		return string(b[:labelRunes]) + "…"
	}
	return string(b)
}

// text is a field's text ("" when absent or null).
func text(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// timeText is an envelope time in the filter's datetime form.
func timeText(t time.Time) []string {
	if t.IsZero() {
		return nil
	}
	return []string{t.UTC().Format(time.RFC3339Nano)}
}

// sortedKeys lists keys by name, for messages.
func sortedKeys(m map[string]filter.Field) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Form decides which delete the flags ask for: true for the bulk --where
// form, false for the one-row form. Both an id and --where, neither, or
// --dry-run without --where are E_INVALID_INPUT, worded for noun.
func Form(noun, id string, where []string, dryRun bool) (bool, error) {
	hasID := strings.TrimSpace(id) != ""
	switch {
	case len(where) > 0 && hasID:
		return false, errs.New(errs.InvalidInput, noun+" delete: give an id or --where, not both")
	case len(where) > 0:
		return true, nil
	case dryRun:
		return false, errs.New(errs.InvalidInput, noun+" delete: --dry-run needs --where")
	case !hasID:
		return false, errs.New(errs.InvalidInput, noun+" delete: give an id or --where").
			WithHint("lore " + noun + " delete <id>, or lore " + noun + " delete --where '<field>=<value>' --dry-run")
	}
	return false, nil
}
