package trash_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/comment"
	"github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/ops/mission"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/ops/tasklist"
	"github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fieldOf is a live row's field as text ("" when unset or null).
func fieldOf(t *testing.T, h *opstest.H, id, field string) string {
	t.Helper()
	x, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := h.App.Get(context.Background(), engine.Ref{ID: x})
	if err != nil || r.Deleted {
		t.Fatalf("get %s: %v (deleted %v)", id, err, r.Deleted)
	}
	v, ok := r.Fields[field]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// listIDs is the ids trash.list prints for input.
func listIDs(t *testing.T, h *opstest.H, input string) []string {
	t.Helper()
	out := []string{}
	for _, it := range listOf(t, h, input).Data {
		out = append(out, it.ID)
	}
	return out
}

// TestTrashListToStore (OV31, DELETION 4.5): --to keeps the rows of one
// store: a row trashed in the repo and one trashed in an attached store
// are each listed under their own store name only; a store that is not
// attached is E_NOT_FOUND; stats counts each store apart.
func TestTrashListToStore(t *testing.T) {
	h := opstest.WithStores(t, []string{"kb"})
	inRepo := trashed(t, h, "repo note", "")
	inKB := h.ID(h.Must(memory.OpAdd, `{"body":"kb note","to":"kb"}`))
	h.MustYes(memory.OpDelete, `{"id":"`+inKB+`"}`)
	for _, c := range []struct {
		in   string
		want []string
	}{
		{``, []string{inRepo, inKB}},
		{`{"to":"repo"}`, []string{inRepo}},
		{`{"to":"kb"}`, []string{inKB}},
	} {
		got := listIDs(t, h, c.in)
		if len(got) != len(c.want) {
			t.Errorf("list %s = %v, want %v", c.in, got, c.want)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(strings.Join(got, ","), w) {
				t.Errorf("list %s = %v, missing %s", c.in, got, w)
			}
		}
	}
	h.Fails(trash.OpList, `{"to":"nowhere"}`, errs.NotFound)
	for _, it := range listOf(t, h, `{"to":"kb"}`).Data {
		if it.Store != "kb" {
			t.Errorf("item %+v under --to kb", it)
		}
	}
	if st := statsOf(t, h); len(st.ByStore) != 2 {
		t.Fatalf("stats by store: %+v", st.ByStore)
	}
}

// TestTrashListWithDependents (OV31, DELETION 3.9): sub-tasks that went to
// the trash with their parent are dependents: the plain list shows only
// the parent and counts two dependents; --with-dependents lists them too
// (and counts none); a filter that drops the root drops its count;
// restoring the root brings the subtree back. A comment is not a
// dependent in 0.1.18 (G-ENGINE-1): it stays live beside a trashed target.
func TestTrashListWithDependents(t *testing.T) {
	h := opstest.New(t)
	root := h.ID(h.MustYes(opstask.OpAdd, `{"title":"has sub-tasks"}`))
	kid := h.ID(h.MustYes(opstask.OpAdd, `{"title":"child","parent":"`+root+`"}`))
	grand := h.ID(h.MustYes(opstask.OpAdd, `{"title":"grandchild","parent":"`+kid+`"}`))
	note := h.ID(h.MustYes(comment.OpAdd, `{"on_table":"tasks","on_id":"`+root+`","body":"a remark"}`))
	h.MustYes(opstask.OpDelete, `{"id":"`+root+`"}`)
	if fieldOf(t, h, note, "body") != "a remark" {
		t.Fatal("the comment left with its target (G-ENGINE-1 keeps it live)")
	}
	plain := listOf(t, h, ``)
	if plain.Count != 1 || plain.Data[0].ID != root || plain.Dependents != 2 {
		t.Fatalf("plain list: %+v", plain)
	}
	all := listOf(t, h, `{"with_dependents":true}`)
	got := map[string]trash.Item{}
	for _, it := range all.Data {
		got[it.ID] = it
	}
	if all.Count != 3 || all.Dependents != 0 || !got[kid].Dependent || got[root].Dependent {
		t.Fatalf("--with-dependents: %+v", all)
	}
	if c, ok := got[grand]; !ok || c.Batch != got[root].Batch {
		t.Fatalf("--with-dependents misses the grandchild in the root's batch: %+v", all.Data)
	}
	if none := listOf(t, h, `{"search":"nothing like this"}`); none.Count != 0 || none.Dependents != 0 {
		t.Fatalf("a filtered-out root still counts its dependent: %+v", none)
	}
	h.Must(trash.OpRestore, `{"target":"`+root+`"}`)
	if l := listOf(t, h, `{"with_dependents":true}`); l.Count != 0 || fieldOf(t, h, grand, schema.FieldTaskParent) != kid {
		t.Fatalf("restoring the root left its dependent in the trash: %+v", l)
	}
}

// purgedParent trashes child alone, then trashes and purges parent, so the
// child sits in the trash with a parent that was deleted for good.
func purgedParent(t *testing.T, h *opstest.H, child, parent, delOp string) {
	t.Helper()
	h.MustYes(opstask.OpDelete, `{"id":"`+child+`"}`)
	h.MustYes(delOp, `{"id":"`+parent+`"}`)
	h.Must(trash.OpPurge, `{"target":"`+parent+`","confirm":true}`)
}

// TestTrashRestoreHomeFlags (OV31, DELETION 4.2): a trashed row whose
// parent, tasklist or mission was purged is refused E_PARENT_PURGED by a plain
// restore and comes back with --no-parent / --parent <id> /
// --no-tasklist / --tasklist <id> / --mission <id>; a new home in the
// trash is E_PARENT_DELETED, a missing one E_NOT_FOUND; a home no
// restored row takes is E_INVALID_INPUT; a refused restore leaves the row in the trash.
func TestTrashRestoreHomeFlags(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, h *opstest.H) (child string)
		flags func(h *opstest.H) string // restore flags (JSON members)
		field string
		want  func(h *opstest.H) string // the field after the restore ("" = cleared)
	}{
		{"no-parent", func(t *testing.T, h *opstest.H) string {
			p := h.ID(h.MustYes(opstask.OpAdd, `{"title":"parent"}`))
			c := h.ID(h.MustYes(opstask.OpAdd, `{"title":"child","parent":"`+p+`"}`))
			purgedParent(t, h, c, p, opstask.OpDelete)
			return c
		}, func(*opstest.H) string { return `"no_parent":true` }, schema.FieldTaskParent, func(*opstest.H) string { return "" }},
		{"new parent", func(t *testing.T, h *opstest.H) string {
			p := h.ID(h.MustYes(opstask.OpAdd, `{"title":"parent"}`))
			c := h.ID(h.MustYes(opstask.OpAdd, `{"title":"child","parent":"`+p+`"}`))
			h.ID(h.MustYes(opstask.OpAdd, `{"title":"adopter"}`))
			purgedParent(t, h, c, p, opstask.OpDelete)
			return c
		}, func(h *opstest.H) string { return `"parent":"` + idOfTitle(h, "adopter") + `"` }, schema.FieldTaskParent,
			func(h *opstest.H) string { return idOfTitle(h, "adopter") }},
		{"no-tasklist", func(t *testing.T, h *opstest.H) string {
			l := h.ID(h.MustYes(tasklist.OpAdd, `{"title":"old list","body":"x"}`))
			c := h.ID(h.MustYes(opstask.OpAdd, `{"title":"listed","tasklist":"`+l+`"}`))
			purgedParent(t, h, c, l, tasklist.OpDelete)
			return c
		}, func(*opstest.H) string { return `"no_tasklist":true` }, "tasklist_id", func(*opstest.H) string { return "" }},
		{"new tasklist", func(t *testing.T, h *opstest.H) string {
			l := h.ID(h.MustYes(tasklist.OpAdd, `{"title":"old list","body":"x"}`))
			h.ID(h.MustYes(tasklist.OpAdd, `{"title":"new list","body":"x"}`))
			c := h.ID(h.MustYes(opstask.OpAdd, `{"title":"listed","tasklist":"`+l+`"}`))
			purgedParent(t, h, c, l, tasklist.OpDelete)
			return c
		}, func(h *opstest.H) string { return `"tasklist":"` + idOfTitle(h, "new list") + `"` }, "tasklist_id",
			func(h *opstest.H) string { return idOfTitle(h, "new list") }},
		{"new mission", func(t *testing.T, h *opstest.H) string {
			m := h.ID(h.MustYes(mission.OpAdd, `{"title":"old goal"}`))
			h.ID(h.MustYes(mission.OpAdd, `{"title":"new goal"}`))
			c := h.ID(h.MustYes(opstask.OpAdd, `{"title":"aimed","mission":"`+m+`"}`))
			purgedParent(t, h, c, m, mission.OpDelete)
			return c
		}, func(h *opstest.H) string { return `"mission":"` + idOfTitle(h, "new goal") + `"` }, "mission_id",
			func(h *opstest.H) string { return idOfTitle(h, "new goal") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := opstest.New(t)
			child := c.setup(t, h)
			h.Fails(trash.OpRestore, `{"target":"`+child+`"}`, errs.ParentPurged)
			if !inTrash(t, h, child) {
				t.Fatal("a refused restore took the row out of the trash")
			}
			h.Must(trash.OpRestore, `{"target":"`+child+`",`+c.flags(h)+`}`)
			if got, want := fieldOf(t, h, child, c.field), c.want(h); got != want {
				t.Fatalf("%s after restore = %q, want %q", c.field, got, want)
			}
		})
	}
	t.Run("refusals", func(t *testing.T) {
		h := opstest.New(t)
		p := h.ID(h.MustYes(opstask.OpAdd, `{"title":"parent"}`))
		c := h.ID(h.MustYes(opstask.OpAdd, `{"title":"child","parent":"`+p+`"}`))
		gone := h.ID(h.MustYes(opstask.OpAdd, `{"title":"in the trash"}`))
		h.MustYes(opstask.OpDelete, `{"id":"`+gone+`"}`)
		purgedParent(t, h, c, p, opstask.OpDelete)
		h.Fails(trash.OpRestore, `{"target":"`+c+`","parent":"`+gone+`"}`, errs.ParentDeleted)
		h.Fails(trash.OpRestore, `{"target":"`+c+`","parent":"tsk_01a1a5aca7f57659be7320c5f4b70011"}`, errs.NotFound)
		if !inTrash(t, h, c) {
			t.Fatal("a refused restore took the row out of the trash")
		}
		mem := trashed(t, h, "no parent here", "")
		h.Fails(trash.OpRestore, `{"target":"`+mem+`","no_parent":true}`, errs.InvalidInput)
		if !inTrash(t, h, mem) {
			t.Fatal("a refused restore took the memory out of the trash")
		}
	})
}

// idOfTitle is the id of the one live task, tasklist or mission titled
// title.
func idOfTitle(h *opstest.H, title string) string {
	for _, table := range []row.Table{schema.TableTasks, schema.TableTasklists, schema.TableMissions} {
		rows, err := h.App.List(context.Background(), store.SourceRepo, table)
		if err != nil {
			continue
		}
		for _, r := range rows {
			if v, ok := r.Fields["title"]; ok && v.String() == title && !r.Deleted {
				return r.ID.String()
			}
		}
	}
	return ""
}

// statsOf runs trash.stats and decodes it.
func statsOf(t *testing.T, h *opstest.H) trash.StatsOut {
	t.Helper()
	var st trash.StatsOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(trash.OpStats, ``))), &st); err != nil {
		t.Fatal(err)
	}
	return st
}
