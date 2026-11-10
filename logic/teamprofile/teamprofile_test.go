package teamprofile

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

func txt(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestStatusOf is the TEAM_PROFILES 2.7 table: status × active, active as
// a bool, as inferred text, absent and null.
func TestStatusOf(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status value.Value
		active value.Value
		want   string
	}{
		{"nothing", nil, nil, schema.ActorStatusActive},
		{"active true", nil, value.BoolOf(true), schema.ActorStatusActive},
		{"active false (deactivate)", nil, value.BoolOf(false), schema.ActorStatusLeft},
		{"active false as text", nil, txt(t, "false"), schema.ActorStatusLeft},
		{"active null", nil, value.Null{}, schema.ActorStatusActive},
		{"status wins over active true", txt(t, schema.ActorStatusArchived), value.BoolOf(true), schema.ActorStatusArchived},
		{"status wins over active false", txt(t, schema.ActorStatusOnLeave), value.BoolOf(false), schema.ActorStatusOnLeave},
		{"status left", txt(t, schema.ActorStatusLeft), value.BoolOf(false), schema.ActorStatusLeft},
		{"status null reads active", value.Null{}, value.BoolOf(false), schema.ActorStatusLeft},
	} {
		f := map[string]value.Value{}
		if tc.status != nil {
			f[schema.FieldActorStatus] = tc.status
		}
		if tc.active != nil {
			f[schema.FieldActorActive] = tc.active
		}
		if got := StatusOf(f); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}
	for s, want := range map[string]bool{schema.ActorStatusActive: true, schema.ActorStatusOnLeave: true, schema.ActorStatusLeft: false, schema.ActorStatusArchived: false} {
		if Pickable(s) != want {
			t.Errorf("Pickable(%s) != %v", s, want)
		}
	}
}

// TestNormalize is the 6.1 table per kind, with the inputs a person pastes:
// case, spaces, URLs, @, trailing slash, unicode, empty.
func TestNormalize(t *testing.T) {
	ok := []struct{ kind, in, want string }{
		{schema.ContactKindEmail, "Mira.Patel@Gmail.com", "mira.patel@gmail.com"},
		{schema.ContactKindEmail, "  mira+lore@acme-agency.co.uk ", "mira+lore@acme-agency.co.uk"},
		{schema.ContactKindHandle, "https://github.com/MiraP/", "mirap"},
		{schema.ContactKindHandle, "HTTPS://GitHub.com/MiraP", "mirap"},
		{schema.ContactKindHandle, "@MiraP", "mirap"},
		{schema.ContactKindHandle, "mira-acme", "mira-acme"},
		{schema.ContactKindHandle, "https://www.linkedin.com/in/mira-patel/", "mira-patel"},
		{schema.ContactKindHandle, "https://x.com/@mira", "mira"},
		{schema.ContactKindHandle, "https://gitlab.com/mira", "mira"},
		{schema.ContactKindPhone, "+90 (532) 000-0000", "+905320000000"},
		{schema.ContactKindPhone, "0532 000 0000", "05320000000"},
		{schema.ContactKindURL, "HTTPS://Example.COM/Path/", "https://example.com/Path"},
		{schema.ContactKindURL, "http://example.com", "http://example.com"},
		{schema.ContactKindText, "  mira#1234 ", "mira#1234"},
		{schema.ContactKindText, "Mīra 🚀", "Mīra 🚀"},
	}
	for _, tc := range ok {
		got, err := Normalize(tc.kind, tc.in)
		if err != nil || got != tc.want {
			t.Errorf("Normalize(%s, %q) = %q, %v; want %q", tc.kind, tc.in, got, err, tc.want)
		}
	}
	bad := []struct {
		kind, in string
		code     errs.Code
	}{
		{schema.ContactKindEmail, "", errs.ContactType},
		{schema.ContactKindEmail, "mira", errs.ContactType},
		{schema.ContactKindEmail, "mira@localhost", errs.ContactType},
		{schema.ContactKindEmail, "a@b@c.com", errs.ContactType},
		{schema.ContactKindEmail, "@acme.com", errs.ContactType},
		{schema.ContactKindEmail, "mira @acme.com", errs.ContactType},
		{schema.ContactKindEmail, "mira@acme.", errs.ContactType},
		{schema.ContactKindHandle, "", errs.ContactType},
		{schema.ContactKindHandle, "@", errs.ContactType},
		{schema.ContactKindHandle, "https://github.com/a/b", errs.ContactType},
		{schema.ContactKindHandle, "two words", errs.ContactType},
		{schema.ContactKindPhone, "+90 532", errs.ContactType},
		{schema.ContactKindPhone, "call me", errs.ContactType},
		{schema.ContactKindURL, "example.com", errs.ContactType},
		{schema.ContactKindURL, "ftp://example.com", errs.ContactType},
		// built at run time so no credential-shaped literal sits in the tree
		{schema.ContactKindURL, "https://" + strings.Join([]string{"user", "pw"}, ":") + "@example.com", errs.URLUserinfo},
		{schema.ContactKindText, "   ", errs.ContactType},
		{"carrier_pigeon", "coo", errs.ContactType},
		{"", "x", errs.ContactType},
	}
	for _, tc := range bad {
		if _, err := Normalize(tc.kind, tc.in); !errs.Has(err, tc.code) {
			t.Errorf("Normalize(%s, %q): want %s, got %v", tc.kind, tc.in, tc.code, err)
		}
	}
	// Run twice: a normalised value normalises to itself.
	for _, tc := range ok {
		once, _ := Normalize(tc.kind, tc.in)
		twice, err := Normalize(tc.kind, once)
		if err != nil || twice != once {
			t.Errorf("Normalize(%s) not idempotent: %q → %q (%v)", tc.kind, once, twice, err)
		}
	}
}

// TestBuiltins pins TP-1 and TP-8, every kind valid, every name unique.
func TestBuiltins(t *testing.T) {
	var names []string
	for _, c := range BuiltinContactTypes() {
		names = append(names, c.Name)
		if !slices.Contains(ContactKinds(), c.Kind) {
			t.Errorf("%s: kind %s not a contact kind", c.Name, c.Kind)
		}
	}
	want := []string{"github", "gitlab", "email", "phone", "linkedin", "x", "slack", "discord", "website"}
	if !slices.Equal(names, want) {
		t.Errorf("contact types %v want %v", names, want)
	}
	var identity []string
	for _, c := range BuiltinContactTypes() {
		if c.Identity {
			identity = append(identity, c.Name)
		}
	}
	if !slices.Equal(identity, []string{"github", "gitlab", "email"}) {
		t.Errorf("identity types %v", identity)
	}
	names = names[:0]
	for _, r := range BuiltinRoles() {
		names = append(names, r.Name)
	}
	if !slices.Equal(names, []string{"lead", "dev", "designer", "qa", "pm", "reviewer", "client"}) {
		t.Errorf("roles %v", names)
	}
	// Fresh slices: a caller's change never leaks into the next call.
	b := BuiltinRoles()
	b[0].Name = "changed"
	if BuiltinRoles()[0].Name != RoleLead {
		t.Error("BuiltinRoles shares its backing array")
	}
	if !slices.Equal(PeriodTypes(), []string{"employee", "contractor", "intern", "client_contact"}) {
		t.Errorf("period types %v", PeriodTypes())
	}
}

func day(s string) time.Time {
	t, err := time.Parse(value.DateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

func actorRow(t *testing.T, kind string, created time.Time, active *bool, leftAt string) row.Row {
	t.Helper()
	id, err := ids.ParseAny("act_01890a5d8a2b7c3d9e0f1a2b3c4d5e6f")
	if err != nil {
		t.Fatal(err)
	}
	r := row.Row{Table: row.Table(schema.TableActors), ID: id, CreatedAt: created, UpdatedAt: created.Add(48 * time.Hour),
		Fields: map[string]value.Value{"kind": txt(t, kind)}}
	if active != nil {
		r.Fields[schema.FieldActorActive] = value.BoolOf(*active)
	}
	if leftAt != "" {
		r.Fields[schema.FieldActorLeftAt] = txt(t, leftAt)
	}
	return r
}

func periodRow(t *testing.T, id string, actor ids.AnyID, start, end string) row.Row {
	t.Helper()
	pid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	f := map[string]value.Value{schema.FieldPeriodActor: txt(t, actor.String()), schema.FieldPeriodType: txt(t, schema.PeriodTypeContractor),
		schema.FieldPeriodStartedOn: txt(t, start)}
	if end != "" {
		f[schema.FieldPeriodEndedOn] = txt(t, end)
	}
	return row.Row{Table: row.Table(schema.TableActorPeriods), ID: pid, Fields: f}
}

// TestPeriodsOf: rows win; a human with none gets the TP-22 period (open,
// or closed on left_at, or on updated_at without left_at); agents and
// row-less creation get none; other actors' and deleted rows are ignored.
func TestPeriodsOf(t *testing.T) {
	created := time.Date(2026, 1, 12, 23, 30, 0, 0, time.UTC)
	yes, no := true, false

	open, err := PeriodsOf(actorRow(t, schema.ActorKindHuman, created, &yes, ""), nil)
	if err != nil || len(open) != 1 || !open[0].Synthesised() || !open[0].Open() || !open[0].StartedOn.Equal(day("2026-01-12")) ||
		open[0].Source != schema.PeriodSourceMigrated || open[0].Type != schema.PeriodTypeEmployee {
		t.Fatalf("active human: %+v %v", open, err)
	}
	gone, err := PeriodsOf(actorRow(t, schema.ActorKindHuman, created, &no, "2026-09-30T17:00:00.000000000Z"), nil)
	if err != nil || len(gone) != 1 || !gone[0].EndedOn.Equal(day("2026-09-30")) {
		t.Fatalf("left human: %+v %v", gone, err)
	}
	noLeftAt, err := PeriodsOf(actorRow(t, schema.ActorKindHuman, created, &no, ""), nil)
	if err != nil || len(noLeftAt) != 1 || !noLeftAt[0].EndedOn.Equal(day("2026-01-14")) {
		t.Fatalf("left without left_at ends on updated_at: %+v %v", noLeftAt, err)
	}
	skew, err := PeriodsOf(actorRow(t, schema.ActorKindHuman, created, &no, "2025-12-01T00:00:00Z"), nil)
	if err != nil || !skew[0].EndedOn.Equal(skew[0].StartedOn) {
		t.Fatalf("left_at before created_at is clamped: %+v %v", skew, err)
	}
	if _, err := PeriodsOf(actorRow(t, schema.ActorKindHuman, created, &no, "yesterday"), nil); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("bad left_at: %v", err)
	}
	for _, kind := range []string{schema.ActorKindAgent, schema.ActorKindCron} {
		if ps, err := PeriodsOf(actorRow(t, kind, created, &yes, ""), nil); err != nil || len(ps) != 0 {
			t.Errorf("%s: %+v %v", kind, ps, err)
		}
	}
	if ps, err := PeriodsOf(actorRow(t, schema.ActorKindHuman, time.Time{}, &yes, ""), nil); err != nil || len(ps) != 0 {
		t.Errorf("no created_at: %+v %v", ps, err)
	}

	a := actorRow(t, schema.ActorKindHuman, created, &yes, "")
	other, _ := ids.ParseAny("act_01890a5d8a2b7c3d9e0f1a2b3c4d5e70")
	tomb := periodRow(t, "apd_01890a5d8a2b7c3d9e0f1a2b3c4d5e03", a.ID, "2020-01-01", "")
	tomb.Deleted = true
	rows := []row.Row{
		periodRow(t, "apd_01890a5d8a2b7c3d9e0f1a2b3c4d5e02", a.ID, "2027-02-01", ""),
		periodRow(t, "apd_01890a5d8a2b7c3d9e0f1a2b3c4d5e01", a.ID, "2026-01-12", "2026-09-30"),
		periodRow(t, "apd_01890a5d8a2b7c3d9e0f1a2b3c4d5e04", other, "2026-01-01", ""),
		tomb,
	}
	ps, err := PeriodsOf(a, rows)
	if err != nil || len(ps) != 2 || !ps[0].StartedOn.Equal(day("2026-01-12")) || !ps[1].Open() || ps[0].Synthesised() {
		t.Fatalf("rows: %+v %v", ps, err)
	}
	if err := CheckPeriods(ps); err != nil {
		t.Fatalf("two clean stints: %v", err)
	}
	bad := periodRow(t, "apd_01890a5d8a2b7c3d9e0f1a2b3c4d5e05", a.ID, "12/01/2026", "")
	if _, err := PeriodsOf(a, []row.Row{bad}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("bad started_on: %v", err)
	}
}

// TestPeriodFields round-trips a period through its row fields.
func TestPeriodFields(t *testing.T) {
	a := actorRow(t, schema.ActorKindHuman, time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC), nil, "")
	p := Period{ActorID: a.ID, Type: schema.PeriodTypeEmployee, StartedOn: day("2026-01-12"), EndedOn: day("2026-09-30"), OrgRole: "Senior engineer", Source: schema.PeriodSourceMigrated}
	f, err := p.Fields()
	if err != nil {
		t.Fatal(err)
	}
	if _, has := f[schema.FieldPeriodNote]; has {
		t.Error("an empty note is written")
	}
	back, err := PeriodsOf(a, []row.Row{{Table: row.Table(schema.TableActorPeriods), ID: a.ID, Fields: f}})
	if err != nil || len(back) != 1 || !back[0].StartedOn.Equal(p.StartedOn) || !back[0].EndedOn.Equal(p.EndedOn) || back[0].OrgRole != p.OrgRole {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	if _, err := (Period{ActorID: a.ID, Type: schema.PeriodTypeEmployee}).Fields(); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("no started_on: %v", err)
	}
}

// TestCheckPeriods: every 2.5 invariant, each broken alone, in any order.
func TestCheckPeriods(t *testing.T) {
	p := func(start, end string) Period {
		x := Period{StartedOn: day(start)}
		if end != "" {
			x.EndedOn = day(end)
		}
		return x
	}
	for _, tc := range []struct {
		name string
		ps   []Period
		ok   bool
		hint string // the refusal's hint ("" = not checked)
	}{
		{"none", nil, true, ""},
		{"one open", []Period{p("2026-01-12", "")}, true, ""},
		{"one day stint", []Period{p("2026-01-12", "2026-01-12")}, true, ""},
		{"closed then open", []Period{p("2026-01-12", "2026-09-30"), p("2026-10-01", "")}, true, ""},
		{"unsorted input", []Period{p("2026-10-01", ""), p("2026-01-12", "2026-09-30")}, true, ""},
		// Its own end is the fix, so the hint names its start, not "a date after the last end".
		{"ends before start", []Period{p("2026-01-12", "2026-01-11")}, false, "end it on or after its start (2026-01-12)"},
		{"ends before start beside a closed stint", []Period{p("2025-01-01", "2025-06-30"), p("2026-01-12", "2026-01-11")}, false, "end it on or after its start (2026-01-12)"},
		{"two open", []Period{p("2026-01-12", ""), p("2026-10-01", "")}, false, ""},
		{"open before closed", []Period{p("2026-01-12", ""), p("2026-10-01", "2026-11-01")}, false, ""},
		{"rejoin on the last day", []Period{p("2026-01-12", "2026-09-30"), p("2026-09-30", "")}, false, ""},
		{"overlap", []Period{p("2026-01-12", "2026-09-30"), p("2026-05-01", "2026-06-01")}, false, "lore actor period list <actor> shows the stints; pick a date after the last end"},
		{"same start", []Period{p("2026-01-12", "2026-01-20"), p("2026-01-12", "")}, false, ""},
	} {
		err := CheckPeriods(tc.ps)
		if tc.ok && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if !tc.ok && !errs.Has(err, errs.PeriodOverlap) {
			t.Errorf("%s: want E_PERIOD_OVERLAP, got %v", tc.name, err)
		}
		var e *errs.Error
		if tc.hint != "" && (!errors.As(err, &e) || e.Hint != tc.hint) {
			t.Errorf("%s: hint of %v, want %q", tc.name, err, tc.hint)
		}
	}
}

// TestDay: the calendar day in t's own location, as midnight UTC.
func TestDay(t *testing.T) {
	ist := time.FixedZone("TRT", 3*3600)
	if got := Day(time.Date(2026, 9, 30, 23, 30, 0, 0, ist)); !got.Equal(day("2026-09-30")) {
		t.Errorf("got %v", got)
	}
}

// TestSpanOverlaps: two spans share a day when each starts on or before
// the other's last day (ended_on is inclusive); an open span has no last
// day. Checked both ways round, since Overlaps must be symmetric.
func TestSpanOverlaps(t *testing.T) {
	d := func(s string) time.Time {
		v, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ended := Span{Start: d("2026-03-01"), End: d("2026-06-30")}
	cases := []struct {
		name string
		o    Span
		want bool
	}{
		{"ended before it", Span{Start: d("2026-01-01"), End: d("2026-02-28")}, false},
		{"ends on its first day", Span{Start: d("2026-01-01"), End: d("2026-03-01")}, true},
		{"inside it", Span{Start: d("2026-04-01"), End: d("2026-04-30")}, true},
		{"covers it", Span{Start: d("2026-01-01"), End: d("2026-12-31")}, true},
		{"starts on its last day", Span{Start: d("2026-06-30"), End: d("2026-07-31")}, true},
		{"starts the day after", Span{Start: d("2026-07-01"), End: d("2026-07-31")}, false},
		{"open, starts before it", Span{Start: d("2026-01-01")}, true},
		{"open, starts on its last day", Span{Start: d("2026-06-30")}, true},
		{"open, starts the day after", Span{Start: d("2026-07-01")}, false},
	}
	for _, c := range cases {
		if got := ended.Overlaps(c.o); got != c.want {
			t.Errorf("%s: Overlaps = %v, want %v", c.name, got, c.want)
		}
		if got := c.o.Overlaps(ended); got != c.want {
			t.Errorf("%s (reversed): Overlaps = %v, want %v", c.name, got, c.want)
		}
	}
	if !(Span{Start: d("2026-01-01")}).Overlaps(Span{Start: d("2027-01-01")}) {
		t.Error("two open spans always overlap")
	}
}
