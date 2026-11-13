package infra_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// TestProjectListTrashFlags pins DELETION 3.2 on project list: a deleted
// project is gone from the plain list; --include-deleted lists it beside
// the live one and --deleted-only alone, each with the one note naming the
// trashed id and the two commands; the live rows never get the note, and
// --json carries the note under "notes".
func TestProjectListTrashFlags(t *testing.T) {
	h := opstest.New(t, ticking(fixedAt))
	old := h.Seed(schema.TableProjects, map[string]string{"name": `"old"`})
	h.MustYes(infra.OpProjectDelete, `{"id":"`+old+`"}`)
	note := "in the trash: " + old + " (lore trash show --id <id>; lore trash restore --target <id>)\n"
	for _, c := range []struct {
		name, in string
		want     string
		note     bool
	}{
		{"plain", ``, h.Project + "  demo\n", false},
		{"include-deleted", `{"include_deleted":true}`, h.Project + "  demo\n" + old + "  old\n", true},
		{"deleted-only", `{"deleted_only":true}`, old + "  old\n", true},
		{"both flags: deleted-only wins", `{"include_deleted":true,"deleted_only":true}`, old + "  old\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := h.Must(infra.OpProjectList, c.in)
			want := c.want
			if c.note {
				want += note
			}
			if got := opstest.Text(res); got != want {
				t.Fatalf("text\n%q\nwant\n%q", got, want)
			}
			raw := h.JSON(res)
			if strings.Contains(raw, `"notes"`) != c.note || (c.note && !strings.Contains(raw, old+" (lore trash show")) {
				t.Fatalf("json notes: %s", raw)
			}
		})
	}
	// --store narrows the stores, not the trash note.
	if got := opstest.Text(h.Must(infra.OpProjectList, `{"include_deleted":true,"store":"repo"}`)); !strings.Contains(got, note) {
		t.Fatalf("store-scoped include-deleted lost the note: %q", got)
	}
	h.MustYes("trash.restore", `{"target":"`+old+`"}`)
	if got := opstest.Text(h.Must(infra.OpProjectList, `{"include_deleted":true}`)); strings.Contains(got, "in the trash") {
		t.Fatalf("a restored project still noted: %q", got)
	}
	if got := opstest.Text(h.Must(infra.OpProjectList, `{"deleted_only":true}`)); got != "(no projects)\n" {
		t.Fatalf("deleted-only after restore %q", got)
	}
}

// hiddenDependent is the Env project delete sees when a run-time kind
// (a table the built-in relation list cannot know) has a restrict row
// pointing at the project: wire.App with PreviewDelete refusing the way
// the engine does. It records each preview's refs.
type hiddenDependent struct {
	*wire.App
	refuse bool
	asked  [][]engine.Ref
}

func (d *hiddenDependent) PreviewDelete(ctx context.Context, refs ...engine.Ref) (engine.DeletePreview, error) {
	d.asked = append(d.asked, slices.Clone(refs))
	if d.refuse {
		return engine.DeletePreview{}, errs.New(errs.HasDependents, "knd_x (svc.project_id) still points at the project")
	}
	return d.App.PreviewDelete(ctx, refs...)
}

// TestProjectDeleteRuntimeRestrictBeforeOwnRow pins r3-projdel: the
// project and the checkout's own repos row are planned as ONE group before
// either is written, so a run-time kind's restrict dependent refuses with
// E_HAS_DEPENDENTS and the own repos row stays live (nothing written); with
// the previewer passing, both go in one batch as before.
func TestProjectDeleteRuntimeRestrictBeforeOwnRow(t *testing.T) {
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	for _, refuse := range []bool{true, false} {
		h, own := mintedCheckout(t)
		env := &hiddenDependent{App: h.App, refuse: refuse}
		i := slices.IndexFunc(infra.Ops(env), func(e op.Entry) bool { return e.Spec().Name == infra.OpProjectDelete })
		if i < 0 {
			t.Fatal("no project.delete entry")
		}
		entry := infra.Ops(env)[i]
		_, err := h.App.Run(context.Background(), entry, op.JSONDecoder{Body: []byte(`{"id":"` + h.Project + `"}`)}, op.Caller{Actor: actor, Yes: true})
		if len(env.asked) != 1 || len(env.asked[0]) != 2 || env.asked[0][0].ID.String() != h.Project || env.asked[0][1].ID.String() != own {
			t.Fatalf("refuse=%v: want one preview of [project, own repos row], got %v", refuse, env.asked)
		}
		gone := deleted(t, h, schema.TableProjects, h.Project) || deleted(t, h, schema.TableRepos, own)
		if refuse {
			if !errs.Has(err, errs.HasDependents) || gone {
				t.Fatalf("hidden dependent: err %v, something deleted %v", err, gone)
			}
			evs, berr := h.App.BatchEvents(context.Background())
			if berr != nil || len(evs) != 0 {
				t.Fatalf("a refused delete wrote batch events: %v %v", evs, berr)
			}
			continue
		}
		if err != nil || !deleted(t, h, schema.TableProjects, h.Project) || !deleted(t, h, schema.TableRepos, own) {
			t.Fatalf("previewer passes: err %v", err)
		}
	}
}
