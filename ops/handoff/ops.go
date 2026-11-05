// Package handoff defines the `handoff` noun's operations on the 0.1.18
// core: add, list, show, ack, delete and search, with v0.1.17's flag
// spellings, --json envelopes and human lines (saas/cmd/cli/coord_views.go
// handoff add, generated_commands.go, archive_commands.go and
// search_entities.go at 4e6ed1c). There is no live ref on the core, so
// --live/--no-live are gone (as for tasks); `lore inbox` keeps reading
// handoffs through the lsync bridge.
package handoff

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/content"
	"github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (handoff.add → `lore handoff add`, POST /v1/handoffs).
const (
	Noun     = "handoff"
	OpAdd    = "handoff.add"
	OpList   = "handoff.list"
	OpShow   = "handoff.show"
	OpAck    = "handoff.ack"
	OpDelete = "handoff.delete"
	OpSearch = "handoff.search"
)

// Handoff fields (schema/builtin_content2.go) and the actor and task
// fields add reads.
const (
	fieldFrom        = "from_actor_id"
	fieldTo          = "to_actor_id"
	fieldStatus      = "status_str"
	fieldTask        = "task_id"
	fieldMission     = "mission_id"
	fieldHandle      = "handle"
	fieldStableKey   = "stable_key"
	fieldDisplayName = "display_name"
)

// Handoff states: open until accepted or declined (coord_views.go);
// `handoff ack` writes acked, as v0.1.17 did.
const (
	StatusOpen  = "open"
	StatusAcked = "acked"
)

// Spellings of an actor reference.
const (
	handlePrefix = "@"
	stableKeySep = ":"
)

// table is the handoffs table.
const table row.Table = schema.TableHandoffs

// Actorer is the optional Env upgrade add uses to record the sender (the
// current identity; wire.App has it). Without it from_actor_id is left
// empty and the envelope's created_by names the same actor.
type Actorer interface {
	Actor(ctx context.Context) (ids.ID[ids.Actor], error)
}

// Ops returns every handoff operation bound to env, for op.NewRegistry.
func Ops(env content.Env) []op.Entry {
	o := ops{c: content.Ops{Env: env, Table: table, Noun: Noun, Prefix: ids.PrefixHandoff}}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Leave a handoff for someone (shows in their inbox)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "hand work over", CLI: `lore handoff add --for @agent-frontend --task tsk_31 "form is half done"`, Want: "hnd_"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List handoffs",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show handoff details",
			Permission: op.PermRead, Access: op.AccessRead, Errors: expand.RevealErrors(content.ReadErrors()), Confirm: expand.RevealConfirm,
		}, o.showExpand),
		op.Define(op.Spec{
			Name: OpAck, Summary: "Acknowledge a handoff (sets status_str=acked)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
		}, o.ack),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a handoff (it goes to the trash; lore trash restore brings it back)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.DeleteErrors(),
			Confirm: op.ConfirmRule{Always: true},
		}, func(ctx context.Context, in DeleteIn) (content.WriteOut, error) {
			return o.c.Delete(ctx, in.ID, in.Reason)
		}),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across handoff body + status",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.SearchErrors(),
		}, o.search),
	}
}

// ops binds the run functions to the shared content steps.
type ops struct{ c content.Ops }

// add leaves one open handoff for --for ("✓ handoff <id> for <who>").
func (o ops) add(ctx context.Context, in AddIn) (content.WriteOut, error) {
	who := in.For
	if who == "" {
		who = in.To
	}
	if strings.TrimSpace(who) == "" {
		return content.WriteOut{}, errs.New(errs.InvalidInput, "--for @handle is required")
	}
	text := in.Text
	if text == "" {
		text = in.Body
	}
	if strings.TrimSpace(text) == "" {
		return content.WriteOut{}, errs.New(errs.EmptyBody, "handoff add: the text is empty").
			WithHint(`example: lore handoff add --for @agent "what is left"`)
	}
	to, err := o.actor(ctx, who)
	if err != nil {
		return content.WriteOut{}, err
	}
	project, err := o.c.ProjectOf(ctx, in.Project)
	if err != nil {
		return content.WriteOut{}, err
	}
	fields, err := content.Texts(Noun, map[string]string{content.FieldBody: text, fieldStatus: StatusOpen})
	if err != nil {
		return content.WriteOut{}, err
	}
	refs := map[string]ids.AnyID{content.FieldProject: project.Any(), fieldTo: to}
	if from := o.me(ctx); !from.IsZero() {
		refs[fieldFrom] = from
	}
	if in.Task != "" {
		task, mission, terr := o.task(ctx, in.Task)
		if terr != nil {
			return content.WriteOut{}, terr
		}
		refs[fieldTask] = task
		if !mission.IsZero() {
			refs[fieldMission] = mission
		}
	}
	for k, id := range refs {
		if fields[k], err = value.RefOf(id); err != nil {
			return content.WriteOut{}, err
		}
	}
	out, err := o.c.Create(ctx, in.Store, fields)
	if err != nil {
		return out, err
	}
	out.Human = content.Mark + " handoff " + out.ID + " for " + who + "\n"
	return out, nil
}

// ack sets status_str=acked ("✓ <id> acked").
func (o ops) ack(ctx context.Context, in IDIn) (content.WriteOut, error) {
	patch, err := content.Texts(Noun, map[string]string{fieldStatus: StatusAcked})
	if err != nil {
		return content.WriteOut{}, err
	}
	return o.c.Patch(ctx, in.ID, patch, func(id string) string { return content.Mark + " " + id + " acked\n" })
}

// me is the current identity's actor id when its actor row exists (the
// engine refuses a dangling actor ref), else zero.
func (o ops) me(ctx context.Context) ids.AnyID {
	a, ok := o.c.Env.(Actorer)
	if !ok {
		return ids.AnyID{}
	}
	me, err := a.Actor(ctx)
	if err != nil || me.IsZero() {
		return ids.AnyID{}
	}
	if _, r, gerr := o.c.Env.Get(ctx, engine.Ref{Table: schema.TableActors, ID: me.Any()}); gerr != nil || r.Deleted {
		return ids.AnyID{}
	}
	return me.Any()
}

// actor resolves --for: an act_ id, an @handle (the actor's handle, or the
// name part of its stable key), a stable key, or a display name (any
// case). No match is E_NOT_FOUND; several are E_INVALID_INPUT naming them.
func (o ops) actor(ctx context.Context, raw string) (ids.AnyID, error) {
	ref := strings.TrimSpace(raw)
	if id, err := ids.Parse[ids.Actor](ref); err == nil {
		return id.Any(), nil
	}
	name := strings.TrimPrefix(ref, handlePrefix)
	if name == "" {
		return ids.AnyID{}, errs.New(errs.InvalidInput, "empty handle")
	}
	rows, err := o.allActors(ctx)
	if err != nil {
		return ids.AnyID{}, err
	}
	var hits []string
	seen := map[string]bool{}
	for _, r := range rows {
		key := content.Field(r, fieldStableKey)
		_, keyName, _ := strings.Cut(key, stableKeySep)
		if content.Field(r, fieldHandle) == name || key == name || keyName == name || strings.EqualFold(content.Field(r, fieldDisplayName), name) {
			if !seen[r.ID.String()] {
				seen[r.ID.String()] = true
				hits = append(hits, r.ID.String())
			}
		}
	}
	switch len(hits) {
	case 0:
		return ids.AnyID{}, errs.New(errs.NotFound, "no actor "+content.QuoteID(ref)).
			WithHint("an actor row exists once that person or agent has written to lore; or pass an act_ id")
	case 1:
		return ids.ParseAny(hits[0])
	}
	return ids.AnyID{}, errs.New(errs.InvalidInput, content.QuoteID(ref)+" matches several actors: "+strings.Join(hits, ", ")).
		WithHint("pass the act_ id")
}

// allActors lists the live actor rows of every attached store.
func (o ops) allActors(ctx context.Context) ([]row.Row, error) {
	var out []row.Row
	for _, src := range o.c.Env.Stores() {
		rows, err := o.c.Env.List(ctx, src, schema.TableActors)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !r.Deleted {
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// task resolves --task to the task id and its mission (zero when none).
func (o ops) task(ctx context.Context, raw string) (ids.AnyID, ids.AnyID, error) {
	id, err := content.ParseID("handoff add --task", ids.PrefixTask, raw)
	if err != nil {
		return ids.AnyID{}, ids.AnyID{}, err
	}
	_, r, err := o.c.Env.Get(ctx, engine.Ref{Table: schema.TableTasks, ID: id})
	if errs.Has(err, errs.NotFound) || (err == nil && r.Deleted) {
		return ids.AnyID{}, ids.AnyID{}, errs.New(errs.NotFound, "--task "+content.QuoteID(raw)+" not found")
	}
	if err != nil {
		return ids.AnyID{}, ids.AnyID{}, err
	}
	if m := content.Field(r, fieldMission); m != "" {
		mid, perr := ids.ParseAny(m)
		if perr == nil {
			return id, mid, nil
		}
	}
	return id, ids.AnyID{}, nil
}

// list returns the live handoffs, every project unless --project.
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	ctx = in.Deleted(ctx, o.c.Table) // DELETION 3.2: --include-deleted / --deleted-only
	project, err := o.c.ProjectFilter(ctx, in.Project)
	if err != nil {
		return ListOut{}, err
	}
	rows, err := o.c.List(ctx, in.Store, func(r row.Row) bool {
		return project == "" || content.Field(r, content.FieldProject) == project
	})
	if err != nil {
		return ListOut{}, err
	}
	rows, notes, err := o.c.Narrow(ctx, rows, in.MetaFlags)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: content.SchemaVersion, Kind: KindList, Data: []Record{}, Notes: notes}
	for _, l := range rows {
		out.Data = append(out.Data, recordOf(o.c.Labeler(), l.Source, l.Row))
	}
	out.Count = len(out.Data)
	return out, nil
}

// show returns one handoff.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	_, src, r, err := o.c.Live(ctx, in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: content.SchemaVersion, Kind: KindShow, Data: recordOf(o.c.Labeler(), src, r)}, nil
}

// search matches the query over body + status in every attached store.
func (o ops) search(ctx context.Context, in SearchIn) (SearchOut, error) {
	sc, repo, err := o.c.ReadScope(ctx, in.Repo, in.NoInherit, in.MasterOnly, in.AllRepos)
	if err != nil {
		return SearchOut{}, err
	}
	project, err := o.c.ProjectFilter(ctx, in.Project)
	if err != nil {
		return SearchOut{}, err
	}
	found, err := o.c.Search(ctx, content.SearchArgs{Query: in.Query, Limit: in.Limit, Columns: in.Columns, IncludeArchived: in.IncludeArchived},
		func(r row.Row) bool {
			return sc.Keeps(content.Field(r, content.FieldRepo), repo) && (project == "" || content.Field(r, content.FieldProject) == project)
		})
	if err != nil {
		return SearchOut{}, err
	}
	out := SearchOut{SchemaVersion: content.SchemaVersion, Kind: KindSearch, Query: in.Query}
	for _, f := range found {
		out.Data = append(out.Data, SearchHit{
			ID: f.Row.ID.String(), Score: f.Score, Snippet: f.Snippet, Store: content.SourceOf(o.c.Labeler(), f.Source), Label: content.Label(o.c.Labeler(), f.Source),
			Row: HitRow{Body: content.Field(f.Row, content.FieldBody), StatusStr: content.Field(f.Row, fieldStatus)},
		})
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of a handoff row.
func recordOf(lab content.Labeler, src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		ProjectID: content.Field(r, content.FieldProject), FromActorID: content.Field(r, fieldFrom),
		ToActorID: content.Field(r, fieldTo), Body: content.Field(r, content.FieldBody),
		StatusStr: content.Field(r, fieldStatus), TaskID: content.Field(r, fieldTask),
		MissionID: content.Field(r, fieldMission), TagIDs: content.TagIDs(r), Store: content.SourceOf(lab, src), Label: content.Label(lab, src),
	}
}
