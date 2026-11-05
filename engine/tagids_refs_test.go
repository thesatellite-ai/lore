package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// danglingTagsRegistry is Builtin with memories.tag_ids' relation marked
// AllowDangling.
func danglingTagsRegistry(t *testing.T) *schema.Registry {
	t.Helper()
	defs := schema.Builtin()
	for i, d := range defs {
		if d.Name != schema.TableMemories {
			continue
		}
		for j, r := range d.Relations {
			if r.Kind == schema.ManyToMany && r.Field == schema.FieldTagIDs {
				defs[i].Relations[j].AllowDangling = true
			}
		}
	}
	reg, err := schema.NewRegistry(defs...)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestTagIDsCheckedLikeBelongsTo (O11): every item of tag_ids is checked
// like a belongs-to ref: a missing tag, an id of another table and a
// non-id item refuse the write (E_RESOURCE_INVALID for a non-id, as any field error); an existing tag passes;
// an empty list passes; with AllowDangling on the relation a missing tag
// is accepted but a non-id item is still refused.
func TestTagIDsCheckedLikeBelongsTo(t *testing.T) {
	ctx := context.Background()
	for _, dangling := range []bool{false, true} {
		var f *fixture
		if dangling {
			f = setup(t, WithRegistry(danglingTagsRegistry(t)))
		} else {
			f = setup(t)
		}
		tag, err := f.eng.Create(ctx, store.SourceRepo, schema.TableTags, map[string]value.Value{"project_id": mustRef(t, f.project), "name": txt("deploy")})
		if err != nil {
			t.Fatal(err)
		}
		mem := f.memory(t, "a memory to point at")
		missing, err := f.eng.ids.NewID(tag.Row.ID.Prefix())
		if err != nil {
			t.Fatal(err)
		}
		list := func(items ...string) value.Value {
			vs := make([]value.Value, len(items))
			for i, s := range items {
				vs[i] = txt(s)
			}
			l, lerr := value.ListOf(value.KindText, vs...)
			if lerr != nil {
				t.Fatal(lerr)
			}
			return l
		}
		cases := []struct {
			name  string
			items []string
			want  errs.Code // "" = accepted
		}{
			{"existing tag", []string{tag.Row.ID.String()}, ""},
			{"empty list", nil, ""},
			{"existing tag twice", []string{tag.Row.ID.String(), tag.Row.ID.String()}, ""},
			{"missing tag", []string{tag.Row.ID.String(), missing.String()}, errs.NotFound},
			{"id of another table", []string{mem.ID.String()}, errs.NotFound},
			{"not an id", []string{"deploy"}, errs.ResourceInvalid},
			{"unicode junk", []string{"tag_ünï"}, errs.ResourceInvalid},
		}
		for _, c := range cases {
			want := c.want
			if dangling && want == errs.NotFound {
				want = "" // AllowDangling honoured for a missing target
			}
			fields := f.memFields("tagged " + c.name)
			fields[schema.FieldTagIDs] = list(c.items...)
			w, werr := f.eng.Create(ctx, store.SourceRepo, schema.TableMemories, fields)
			switch {
			case want == "" && werr != nil:
				t.Errorf("dangling=%v %s: refused: %v", dangling, c.name, werr)
			case want != "" && !errs.Has(werr, want):
				t.Errorf("dangling=%v %s: %v, want %s", dangling, c.name, werr, want)
			case want != "" && !w.Row.ID.IsZero():
				t.Errorf("dangling=%v %s: a refused write returned a row", dangling, c.name)
			}
			if want == errs.NotFound && werr != nil && !strings.Contains(werr.Error(), "tag_ids") {
				t.Errorf("%s: message does not name the field: %v", c.name, werr)
			}
		}
		// Update checks only the patched list, with the same rule.
		fields := f.memFields("to patch")
		w, err := f.eng.Create(ctx, store.SourceRepo, schema.TableMemories, fields)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.eng.Update(ctx, Ref{ID: w.Row.ID}, map[string]value.Value{schema.FieldTagIDs: list(missing.String())})
		if dangling && err != nil {
			t.Errorf("dangling update refused: %v", err)
		}
		if !dangling && !errs.Has(err, errs.NotFound) {
			t.Errorf("update to a missing tag: %v, want E_NOT_FOUND", err)
		}
	}
}
