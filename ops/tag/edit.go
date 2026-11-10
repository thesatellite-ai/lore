package tag

// tag.edit (`lore tag edit <tag> [--name] [--color] [--globs]`,
// CONTEXT_BUDGET 6.10): change a tag's label, color or topic glob set.
//
//	edit ─► resolve: tag_ id (Get; deleted → lookup.Gone: E_IN_TRASH / E_NOT_FOUND)
//	        or a name in the current project (tagref.Find, nearest store first)
//	     ─► patch only the given fields (nothing given → E_INVALID_INPUT)
//	     ─► env.Update (the engine's one write path)
//
// A topic is a tag: its id is permanent, so a rename changes no tagged row
// (6.10). The glob set is what `lore context --for-file` matches to pull a
// topic's rows for a path; "-" clears it.

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/tagref"
	"github.com/thesatellite-ai/lore/schema"
)

// OpEdit is `lore tag edit`.
const OpEdit = "tag.edit"

// fieldGlobs is the tag's topic glob set (schema: tags.globs).
const fieldGlobs = "globs"

// clearValue clears --color or --globs.
const clearValue = "-"

// EditIn is the input of tag.edit.
type EditIn struct {
	ID    string `json:"id" arg:"0" desc:"the tag: its id (tag_…) or its name"`
	Name  string `json:"name,omitempty" flag:"name" desc:"new name (snake_case); tagged rows keep the tag (ids are permanent)"`
	Color string `json:"color,omitempty" flag:"color" desc:"new hex color #RRGGBB (- clears it)"`
	Globs string `json:"globs,omitempty" flag:"globs" desc:"the topic's glob set, comma separated (services/billing/**); - clears it"`
}

// EditOut is the output of tag.edit.
type EditOut struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	Globs string `json:"globs,omitempty"`
}

// Text is the confirmation line.
func (o EditOut) Text() string {
	s := "✓ tag " + o.Name + " " + o.ID
	if o.Globs != "" {
		s += " (globs " + o.Globs + ")"
	}
	return s + "\n"
}

// editEntry is the tag.edit definition (registered by Ops).
func (o ops) editEntry() op.Entry {
	return op.Define(op.Spec{
		Name: OpEdit, Summary: "Change a tag's name, color or topic glob set",
		Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(writeErrors), errs.InTrash),
		Examples: []op.Example{{Title: "give a topic its files", CLI: "lore tag edit billing --globs \"services/billing/**\"", Want: "globs"}},
	}, o.edit)
}

// edit patches the given fields of one tag.
func (o ops) edit(ctx context.Context, in EditIn) (EditOut, error) {
	ref, err := o.resolve(ctx, strings.TrimSpace(in.ID))
	if err != nil {
		return EditOut{}, err
	}
	patch := map[string]value.Value{}
	if n := strings.TrimSpace(in.Name); n != "" {
		if err = checkName(n); err != nil {
			return EditOut{}, err
		}
		if patch[fieldName], err = value.TextOf(n); err != nil {
			return EditOut{}, err
		}
	}
	if c := strings.TrimSpace(in.Color); c != "" && c != clearValue {
		if err = checkColor(c); err != nil {
			return EditOut{}, err
		}
	}
	if err = setOrClear(patch, fieldColor, strings.TrimSpace(in.Color)); err != nil {
		return EditOut{}, err
	}
	if err = setOrClear(patch, fieldGlobs, normGlobs(in.Globs)); err != nil {
		return EditOut{}, err
	}
	if len(patch) == 0 {
		return EditOut{}, errs.New(errs.InvalidInput, "tag edit: nothing to change").WithHint("pass --name, --color or --globs")
	}
	w, err := o.env.Update(ctx, ref, patch)
	if err != nil {
		return EditOut{}, err
	}
	return EditOut{ID: w.Row.ID.String(), Name: field(w.Row, fieldName), Color: field(w.Row, fieldColor), Globs: field(w.Row, fieldGlobs)}, nil
}

// setOrClear puts v into patch[f]: "" leaves f alone, "-" clears it.
func setOrClear(patch map[string]value.Value, f, v string) error {
	switch v {
	case "":
		return nil
	case clearValue:
		patch[f] = value.Null{}
		return nil
	}
	t, err := value.TextOf(v)
	patch[f] = t
	return err
}

// normGlobs is the glob list in one spelling ("a/**,b/*.sql"); "-" stays.
func normGlobs(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == clearValue {
		return raw
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' })
	return strings.Join(parts, ",")
}

// resolve finds the live tag raw names: a tag_ id, else a name in the
// current project. Any value that names no live tag is E_NOT_FOUND
// (lookup rule), a trashed one E_IN_TRASH.
func (o ops) resolve(ctx context.Context, raw string) (engine.Ref, error) {
	notFound := errs.New(errs.NotFound, "tag "+quote(raw)+" not found").WithHint("list them with `lore tag list`")
	if id, err := ids.ParseAny(raw); err == nil {
		if id.Prefix() != ids.PrefixTag {
			return engine.Ref{}, lookup.BadID("tag: "+quote(raw)+" is not a tag id (tag_…)", nil)
		}
		ref := engine.Ref{Table: schema.TableTags, ID: id}
		src, r, gerr := o.env.Get(ctx, ref)
		if gerr == nil && r.Deleted {
			gerr = lookup.Gone(ctx, o.env, ref, notFound)
		}
		if gerr != nil {
			return engine.Ref{}, gerr
		}
		ref.Store = src
		return ref, nil
	}
	project, err := o.env.Project(ctx)
	if err != nil {
		return engine.Ref{}, err
	}
	found, err := tagref.Find(ctx, o.env, project.String(), raw)
	if err != nil {
		return engine.Ref{}, err
	}
	if len(found) == 0 {
		return engine.Ref{}, notFound
	}
	ref := engine.Ref{Table: schema.TableTags, ID: found[0]}
	src, _, err := o.env.Get(ctx, ref)
	if err != nil {
		return engine.Ref{}, err
	}
	ref.Store = src
	return ref, nil
}
