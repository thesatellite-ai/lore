package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// profileSource is the real actors and profile table definitions over
// in-memory rows in one store; no mocks of any module path.
type profileSource struct{ rows map[row.Table][]row.Row }

func (p profileSource) Tables() []schema.TableDef {
	var out []schema.TableDef
	for _, d := range schema.Builtin() {
		switch string(d.Name) {
		case schema.TableActors, schema.TableProjects, schema.TableActorContacts, schema.TableActorPeriods, schema.TableProjectMembers:
			out = append(out, d)
		}
	}
	return out
}
func (p profileSource) Stores() []store.Source { return []store.Source{"org"} }
func (p profileSource) List(_ context.Context, _ store.Source, t row.Table) ([]row.Row, error) {
	return p.rows[t], nil
}

// profileID is a fresh id of tag T.
func profileID[T ids.Tag](t *testing.T) ids.AnyID {
	t.Helper()
	id, err := ids.New[T]()
	if err != nil {
		t.Fatal(err)
	}
	return id.Any()
}

// profileFields builds a row's fields from text pairs; "true"/"false" become bools
// and a []string becomes a text list.
func profileFields(t *testing.T, kv map[string]string, lists map[string][]string) map[string]value.Value {
	t.Helper()
	out := map[string]value.Value{}
	for k, v := range kv {
		if v == "true" || v == "false" {
			out[k] = value.BoolOf(v == "true")
			continue
		}
		x, err := value.TextOf(v)
		if err != nil {
			t.Fatal(err)
		}
		out[k] = x
	}
	for k, items := range lists {
		vs := make([]value.Value, 0, len(items))
		for _, s := range items {
			x, err := value.TextOf(s)
			if err != nil {
				t.Fatal(err)
			}
			vs = append(vs, x)
		}
		l, err := value.ListOf(value.KindText, vs...)
		if err != nil {
			t.Fatal(err)
		}
		out[k] = l
	}
	return out
}

func profileRow(table string, id ids.AnyID, f map[string]value.Value) row.Row {
	return row.Row{Table: row.Table(table), ID: id, Fields: f, CreatedAt: time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)}
}

// An actor with two contacts, two periods and one membership shows every
// section with its values; another actor's contact is not shown; a deleted
// contact is not shown.
func TestProfileBlockSections(t *testing.T) {
	mira, other := profileID[ids.Actor](t), profileID[ids.Actor](t)
	prj := profileID[ids.Project](t)
	actor := profileRow(schema.TableActors, mira, profileFields(t, map[string]string{
		"display_name": "Mira Patel", "kind": schema.ActorKindHuman, "title": "Backend engineer",
		"timezone": "Europe/Istanbul", "location": "Ankara, TR", "status": schema.ActorStatusOnLeave, "leave_until": "2026-12-01",
	}, nil))
	c1 := profileRow(schema.TableActorContacts, profileID[ids.ActorContact](t), profileFields(t, map[string]string{
		"actor_id": mira.String(), "type": "github", "value": "mirap", "value_norm": "mirap", "primary": "true", "identity": "true",
	}, nil))
	c2 := profileRow(schema.TableActorContacts, profileID[ids.ActorContact](t), profileFields(t, map[string]string{
		"actor_id": mira.String(), "type": "email", "value": "mira.patel@gmail.com", "value_norm": "mira.patel@gmail.com", "label": "personal",
	}, nil))
	foreign := profileRow(schema.TableActorContacts, profileID[ids.ActorContact](t), profileFields(t, map[string]string{
		"actor_id": other.String(), "type": "email", "value": "someone@else.com", "value_norm": "someone@else.com",
	}, nil))
	gone := profileRow(schema.TableActorContacts, profileID[ids.ActorContact](t), nil)
	gone.Deleted = true
	p1 := profileRow(schema.TableActorPeriods, profileID[ids.ActorPeriod](t), profileFields(t, map[string]string{
		"actor_id": mira.String(), "type": schema.PeriodTypeContractor, "started_on": "2023-01-02", "ended_on": "2023-12-31", "end_reason": "contract ended",
	}, nil))
	p2 := profileRow(schema.TableActorPeriods, profileID[ids.ActorPeriod](t), profileFields(t, map[string]string{
		"actor_id": mira.String(), "type": schema.PeriodTypeEmployee, "started_on": "2024-03-01", "org_role": "Senior engineer",
	}, nil))
	m1 := profileRow(schema.TableProjectMembers, profileID[ids.ProjectMember](t), profileFields(t, map[string]string{
		"actor_id": mira.String(), "project_id": prj.String(), "started_on": "2024-04-01",
	}, map[string][]string{"roles": {"dev", "reviewer"}}))
	proj := profileRow(schema.TableProjects, prj, profileFields(t, map[string]string{"name": "billing"}, nil))

	src := profileSource{rows: map[row.Table][]row.Row{
		schema.TableActors:         {actor},
		schema.TableActorContacts:  {c1, c2, foreign, gone},
		schema.TableActorPeriods:   {p2, p1},
		schema.TableProjectMembers: {m1},
		schema.TableProjects:       {proj},
	}}
	out, err := run(t, src, Options{Kind: "actor"}, "1", "q")
	if err != nil {
		t.Fatal(err)
	}
	block := out[strings.Index(out, "\n  profile\n"):]
	t.Log(block)
	for _, want := range []string{
		"status     on_leave (? – 2026-12-01)",
		"title      Backend engineer",
		"timezone   Europe/Istanbul",
		"contacts (2)",
		"email      mira.patel@gmail.com  [personal]",
		"github     mirap  [primary, identity]",
		"company periods (2)",
		"contractor     2023-01-02 – 2023-12-31  ended: contract ended",
		"employee       2024-03-01 – now  Senior engineer",
		"projects (1)",
		"current billing (" + prj.String() + ")  dev, reviewer  2024-04-01 – now",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("profile block lacks %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "someone@else.com") || strings.Contains(block, "from active/left_at") {
		t.Errorf("foreign contact or a synthesised period shown:\n%s", block)
	}
	// Contacts sort by type: email before github; periods oldest first.
	if strings.Index(block, "mira.patel@gmail.com") > strings.Index(block, "mirap  [") ||
		strings.Index(block, "contractor") > strings.Index(block, "employee") {
		t.Errorf("order:\n%s", block)
	}
}

// A migrated v0.1.17 actor (active false, no profile rows) shows status
// left, its synthesised period ended on left_at, and every empty state;
// memberships list current before ended.
func TestProfileEmptyAndSynthesised(t *testing.T) {
	id := profileID[ids.Actor](t)
	actor := profileRow(schema.TableActors, id, profileFields(t, map[string]string{
		"display_name": "Old Hand", "kind": schema.ActorKindHuman, "active": "false", "left_at": "2025-05-20T12:00:00Z",
	}, nil))
	out, err := run(t, profileSource{rows: map[row.Table][]row.Row{schema.TableActors: {actor}}}, Options{Kind: "actor"}, "1", "q")
	if err != nil {
		t.Fatal(err)
	}
	block := out[strings.Index(out, "\n  profile\n"):]
	for _, want := range []string{
		"status     left",
		"(no profile fields set)",
		"contacts (0)\n    (none)",
		"company periods (1)",
		"employee       2024-03-01 – 2025-05-20  (from active/left_at, no period row yet)",
		"projects (0)\n    (none)",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("profile block lacks %q:\n%s", want, block)
		}
	}
}

// Memberships: current first, then ended; an unattached project shows its id.
func TestProfileMembershipOrder(t *testing.T) {
	a := profileID[ids.Actor](t)
	p1, p2 := profileID[ids.Project](t), profileID[ids.Project](t)
	actor := profileRow(schema.TableActors, a, profileFields(t, map[string]string{"kind": schema.ActorKindAgent}, nil))
	ended := profileRow(schema.TableProjectMembers, profileID[ids.ProjectMember](t), profileFields(t, map[string]string{
		"actor_id": a.String(), "project_id": p1.String(), "started_on": "2025-01-01", "ended_on": "2025-02-01", "end_reason": schema.MemberEndReasonOffboarded,
	}, map[string][]string{"roles": {"qa"}}))
	current := profileRow(schema.TableProjectMembers, profileID[ids.ProjectMember](t), profileFields(t, map[string]string{
		"actor_id": a.String(), "project_id": p2.String(), "started_on": "2025-06-01",
	}, map[string][]string{"roles": {"lead"}}))
	got := renderProfile(actor, nil, nil, []row.Row{ended, current}, map[string]string{})
	ci, ei := strings.Index(got, "current "+p2.String()), strings.Index(got, "ended   "+p1.String()+"  qa  2025-01-01 – 2025-02-01  ended: offboarded")
	if ci < 0 || ei < 0 || ci > ei {
		t.Fatalf("membership order or text:\n%s", got)
	}
	if !strings.Contains(got, "company periods (0)\n    (none)") {
		t.Fatalf("an agent gets no synthesised period:\n%s", got)
	}
}

// Only an actors row screen carries the profile block.
func TestProfileOnlyOnActors(t *testing.T) {
	src := fakeSource{rows: map[row.Table][]row.Row{"memories": {memRow(t, "hello", "x")}}}
	out, err := run(t, src, Options{Kind: "memory"}, "1", "q")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\n  profile\n") {
		t.Fatalf("profile block on a memories row:\n%s", out)
	}
}
