package engine

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// nrID is a made-up UUIDv7-shaped id with prefix p.
func nrID(t *testing.T, p string, n int) ids.AnyID {
	t.Helper()
	id, err := ids.ParseAny(p + "_019a0000000070008000" + strings.Repeat("0", 11) + string(rune('0'+n)))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// nrText is s as a text value.
func nrText(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestResolveNameRefsTasklistByName (devco D-766, H-25): an old-format
// task whose tasklist_id is a list's title, not an id, resolves to that
// list's id in its own project (exact, then ignoring case); a name no
// list of the project has, or two lists share, is cleared; an id, a
// ref and v0.1.17's "" are left alone; each change is one warning line.
func TestResolveNameRefsTasklistByName(t *testing.T) {
	t.Parallel()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	prj, other := nrID(t, "prj", 1), nrID(t, "prj", 2)
	backlog, dup1, dup2, elsewhere := nrID(t, "tlt", 3), nrID(t, "tlt", 4), nrID(t, "tlt", 5), nrID(t, "tlt", 6)
	list := func(id, project ids.AnyID, title string) row.Row {
		return row.Row{Table: schema.TableTasklists, ID: id, Fields: map[string]value.Value{
			"project_id": nrText(t, project.String()), "title": nrText(t, title),
		}}
	}
	x := NewNameRefIndex([]row.Row{
		list(backlog, prj, "Backlog"), list(dup1, prj, "Twice"), list(dup2, prj, "Twice"),
		list(elsewhere, other, "Elsewhere"),
	})
	task := func(n int, ref value.Value) row.Row {
		f := map[string]value.Value{"project_id": nrText(t, prj.String()), "title": nrText(t, "t")}
		if ref != nil {
			f["tasklist_id"] = ref
		}
		return row.Row{Table: schema.TableTasks, ID: nrID(t, "tsk", n), Fields: f}
	}
	ref, err := value.RefOf(backlog)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		in      value.Value
		wantID  ids.AnyID // zero: absent after
		cleared bool
		line    string // "" = no fix
	}{
		{"exact title", nrText(t, "Backlog"), backlog, false, `now the task_lists row ` + backlog.String() + ` named "Backlog"`},
		{"case folded", nrText(t, "backlog"), backlog, false, `now the task_lists row ` + backlog.String()},
		{"no such list", nrText(t, "someday"), ids.AnyID{}, true, "cleared: no task_lists row has that name"},
		{"two lists share it", nrText(t, "Twice"), ids.AnyID{}, true, "cleared: 2 task_lists rows have that name"},
		{"another project's list", nrText(t, "Elsewhere"), ids.AnyID{}, true, "cleared: no task_lists row"},
		{"an id as text", nrText(t, backlog.String()), backlog, false, ""},
		{"a ref", ref, backlog, false, ""},
		{"v0.1.17 empty", nrText(t, ""), ids.AnyID{}, false, ""},
	}
	for i, c := range cases {
		in := task(i+1, c.in)
		got, fixes := ResolveNameRefs(reg, x, in)
		if c.line == "" {
			if len(fixes) != 0 {
				t.Errorf("%s: fixes %+v, want none", c.name, fixes)
			}
			continue
		}
		if len(fixes) != 1 || !strings.Contains(fixes[0].Line(), c.line) || !strings.Contains(fixes[0].Line(), `tasklist_id "`) {
			t.Fatalf("%s: fixes %+v, want one line with %q", c.name, fixes, c.line)
		}
		v, has := got.Fields["tasklist_id"]
		switch {
		case c.cleared && has:
			t.Errorf("%s: tasklist_id = %v, want it cleared", c.name, v)
		case !c.cleared:
			r, ok := v.(value.Ref)
			if !ok || r.ID() != c.wantID {
				t.Errorf("%s: tasklist_id = %#v, want the ref %s", c.name, v, c.wantID)
			}
		}
		if _, ok := in.Fields["tasklist_id"].(value.Text); !ok {
			t.Errorf("%s: the input row was changed", c.name)
		}
	}
}

// TestResolveNameRefsRequiredKept: a required ref holding a name no row
// has is left as is (clearing it would make the row invalid) and said.
func TestResolveNameRefsRequiredKept(t *testing.T) {
	t.Parallel()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	r := row.Row{Table: schema.TableTasks, ID: nrID(t, "tsk", 1), Fields: map[string]value.Value{
		"project_id": nrText(t, "home"), "title": nrText(t, "t"),
	}}
	got, fixes := ResolveNameRefs(reg, NameRefIndex{}, r)
	if len(fixes) != 1 || !fixes[0].Kept || !strings.Contains(fixes[0].Line(), "left as is") {
		t.Fatalf("fixes = %+v, want project_id kept and said", fixes)
	}
	if v, ok := got.Fields["project_id"].(value.Text); !ok || v.String() != "home" {
		t.Fatalf("project_id = %#v, want it kept", got.Fields["project_id"])
	}
}
