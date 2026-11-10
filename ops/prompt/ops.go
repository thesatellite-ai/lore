// Package prompt defines the `prompt` noun's operations on the 0.1.18
// core: add, list, show, edit, archive, unarchive, delete and search, with
// v0.1.17's flag spellings, --json envelopes and human lines
// (saas/cmd/cli/generated_commands.go and friends at 4e6ed1c).
package prompt

import (
	"context"
	"encoding/json"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/creator"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/content"
	"github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/ops/internal/metashow"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (prompt.add → `lore prompt add`, POST /v1/prompts).
const (
	Noun        = "prompt"
	OpAdd       = "prompt.add"
	OpList      = "prompt.list"
	OpShow      = "prompt.show"
	OpEdit      = "prompt.edit"
	OpArchive   = "prompt.archive"
	OpUnarchive = "prompt.unarchive"
	OpDelete    = "prompt.delete"
	OpSearch    = "prompt.search"
)

// Prompt fields (schema/builtin_content.go).
const (
	fieldDescription = "description"
	fieldArgsSchema  = "args_schema"
)

// table is the prompts table.
const table row.Table = schema.TablePrompts

// Ops returns every prompt operation bound to env, for op.NewRegistry.
func Ops(env content.Env) []op.Entry {
	o := ops{c: content.Ops{Env: env, Table: table, Noun: Noun, Prefix: ids.PrefixPrompt}}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a new prompt",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "add a prompt", CLI: `lore prompt add --title "Release" --body "1. tag 2. push"`, Want: "prm_"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List prompts",
			Examples:   []op.Example{{Title: "list as JSON", CLI: "lore prompt list --json", Want: `"data"`}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show prompt details",
			Examples:   []op.Example{{Title: "show one as JSON", CLI: "lore prompt show prm_<id> --json", Want: `"data"`}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: expand.RevealErrors(content.ReadErrors()), Confirm: expand.RevealConfirm,
		}, o.showExpand),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit prompt fields",
			Examples:   []op.Example{{Title: "change the body", CLI: `lore prompt edit prm_<id> --body "the new text"`, Want: "prm_"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpArchive, Summary: "Archive a prompt (soft-delete via archived_at)",
			Examples:   []op.Example{{Title: "archive one", CLI: "lore prompt archive prm_<id>", Want: "archived"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
		}, func(ctx context.Context, in IDIn) (content.WriteOut, error) { return o.c.Archive(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpUnarchive, Summary: "Unarchive a prompt (clear archived_at)",
			Examples:   []op.Example{{Title: "bring it back", CLI: "lore prompt unarchive prm_<id>", Want: "unarchived"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
		}, func(ctx context.Context, in IDIn) (content.WriteOut, error) { return o.c.Unarchive(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a prompt (to the trash; archive is usually what you want)",
			Examples:   []op.Example{{Title: "delete without a terminal", CLI: "lore prompt delete prm_<id> --yes", Want: "prm_"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.DeleteErrors(),
			Confirm: op.ConfirmRule{Always: true},
		}, func(ctx context.Context, in DeleteIn) (content.WriteOut, error) {
			return o.c.Delete(ctx, in.ID, in.Reason)
		}).Asks(func(ctx context.Context, in DeleteIn) (string, error) { return o.c.DeleteQuestion(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across prompt name + body",
			Examples:   []op.Example{{Title: "search as JSON", CLI: `lore prompt search "retry" --json`, Want: `"data"`}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.SearchErrors(),
		}, o.search),
	}
}

// ops binds the run functions to the shared content steps.
type ops struct{ c content.Ops }

// description is v0.1.17's knowledgeDescription: --description, else the
// title; one of them must not be blank.
func description(desc, title string) (string, error) {
	d := strings.TrimSpace(desc)
	if d == "" {
		d = strings.TrimSpace(title)
	}
	if d == "" {
		return "", errs.New(errs.InvalidInput, "--description (or --title) must not be empty")
	}
	return d, nil
}

// add creates one prompt in the target store.
func (o ops) add(ctx context.Context, in AddIn) (content.WriteOut, error) {
	if err := content.Required("title", in.Title); err != nil {
		return content.WriteOut{}, err
	}
	desc, err := description(in.Description, in.Title)
	if err != nil {
		return content.WriteOut{}, err
	}
	body, err := content.Body(Noun, in.Body, in.Text)
	if err != nil {
		return content.WriteOut{}, err
	}
	project, err := o.c.ProjectOf(ctx, in.Project)
	if err != nil {
		return content.WriteOut{}, err
	}
	if err = checkArgsSchema("add", in.ArgsSchema); err != nil {
		return content.WriteOut{}, err
	}
	fields, err := content.Texts(Noun, map[string]string{
		content.FieldName: in.Title, fieldDescription: desc, content.FieldBody: body,
		fieldArgsSchema: in.ArgsSchema,
	})
	if err != nil {
		return content.WriteOut{}, err
	}
	if fields[content.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return content.WriteOut{}, err
	}
	if v := creator.Ref(ctx, o.c.Env); v != nil {
		fields[content.FieldCreatedBy] = v // the calling actor, never caller-set (INTEG-AUDIT-1)
	}
	return o.c.Create(ctx, in.To, fields)
}

// list returns the live prompts (archived ones too, as v0.1.17).
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
		out.Data = append(out.Data, withMeta(ctx, o.c.Env, recordOf(o.c.Labeler(), l.Source, l.Row), l.Row))
	}
	out.Count = len(out.Data)
	return out, nil
}

// show returns one prompt.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	_, src, r, err := o.c.Live(ctx, in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: content.SchemaVersion, Kind: KindShow, Data: withMeta(ctx, o.c.Env, recordOf(o.c.Labeler(), src, r), r)}, nil
}

// edit patches the fields whose flags were given.
func (o ops) edit(ctx context.Context, in EditIn) (content.WriteOut, error) {
	if err := checkArgsSchema("edit", in.ArgsSchema); err != nil {
		return content.WriteOut{}, err
	}
	name, err := content.EditName(Noun, in.Name, in.Title)
	if err != nil {
		return content.WriteOut{}, err
	}
	patch, err := content.Texts(Noun, map[string]string{content.FieldName: name, content.FieldBody: in.Body, fieldDescription: in.Description, fieldArgsSchema: in.ArgsSchema})
	if err != nil {
		return content.WriteOut{}, err
	}
	if len(patch) == 0 && !engine.MetaBound(ctx) {
		return content.WriteOut{}, errs.New(errs.InvalidInput, "prompt edit: nothing to change").
			WithHint("give at least one of --name (or --title), --body, --description, --args-schema")
	}
	return o.c.Patch(ctx, in.ID, patch, func(id string) string { return content.Mark + " prompt " + id + " updated\n" })
}

// search matches the query over name + body in every attached store.
func (o ops) search(ctx context.Context, in SearchIn) (SearchOut, error) {
	project, err := o.c.ProjectFilter(ctx, in.Project)
	if err != nil {
		return SearchOut{}, err
	}
	found, err := o.c.Search(ctx, content.SearchArgs{Query: in.Query, Limit: in.Limit, Columns: in.Columns, IncludeArchived: in.IncludeArchived, Store: in.Store},
		func(r row.Row) bool { return project == "" || content.Field(r, content.FieldProject) == project })
	if err != nil {
		return SearchOut{}, err
	}
	out := SearchOut{SchemaVersion: content.SchemaVersion, Kind: KindSearch, Query: in.Query}
	for _, f := range found {
		out.Data = append(out.Data, SearchHit{
			ID: f.Row.ID.String(), Score: f.Score, Snippet: f.Snippet, Store: content.SourceOf(o.c.Labeler(), f.Source), Label: content.Label(o.c.Labeler(), f.Source),
			Row: HitRow{Body: content.Field(f.Row, content.FieldBody), Name: content.Field(f.Row, content.FieldName)},
		})
		hm := metashow.Row(ctx, o.c.Env, f.Row) // MQ13: metadata on every read
		out.Data[len(out.Data)-1].Meta, out.Data[len(out.Data)-1].MetaMasked = hm.Meta, hm.MetaMasked
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of a prompt row.
func recordOf(lab content.Labeler, src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy,
		ArchivedAt: content.Time(r, content.FieldArchivedAt),
		ProjectID:  content.Field(r, content.FieldProject), Name: content.Field(r, content.FieldName),
		Description: content.Field(r, fieldDescription), Body: content.Field(r, content.FieldBody),
		ArgsSchema: content.Field(r, fieldArgsSchema), CreatedByActorID: content.CreatedBy(r),
		TagIDs: content.TagIDs(r), Store: content.SourceOf(lab, src), Label: content.Label(lab, src),
	}
}

// checkArgsSchema refuses an --args-schema that is not a JSON object
// (G-CONTENT-2): the field is the JSON schema of the {{args.x}}
// placeholders, so text that no reader can parse is refused before
// anything is written, as external-source --config is. "" (not given)
// passes.
func checkArgsSchema(verb, raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &obj); err != nil || obj == nil { // JSON null decodes into a nil map
		return errs.New(errs.InvalidInput, "prompt "+verb+": --args-schema is not a JSON object").
			WithHint(`give a JSON schema object, e.g. --args-schema '{"type":"object","properties":{"name":{"type":"string"}}}'`)
	}
	return nil
}
