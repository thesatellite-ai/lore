package teamaccess

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
)

func mustID[T ids.Tag](t *testing.T, p ids.Prefix, n int) ids.ID[T] {
	t.Helper()
	id, err := ids.Parse[T](fmt.Sprintf("%s_%012x70008%015x", p, 0x01890a5d8a2b, n))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func date(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestRolesAt is the input matrix of the pure role read: open and ended
// stints, the day before the start, the last day of a stint (inclusive),
// the day after, another actor, another project, and two open stints
// after a merge (union, no duplicates).
func TestRolesAt(t *testing.T) {
	t.Parallel()
	mira, carol := mustID[ids.Actor](t, ids.PrefixActor, 1), mustID[ids.Actor](t, ids.PrefixActor, 2)
	hub, nw := mustID[ids.Project](t, ids.PrefixProject, 3), mustID[ids.Project](t, ids.PrefixProject, 4)
	ms := []Membership{
		{Project: hub, Actor: mira, Roles: []string{"dev"}, StartedOn: date("2026-01-12"), EndedOn: date("2026-03-31")},
		{Project: hub, Actor: mira, Roles: []string{"reviewer", "dev"}, StartedOn: date("2026-05-01")},
		{Project: hub, Actor: mira, Roles: []string{"qa"}, StartedOn: date("2026-06-01")}, // a second open stint after a merge
		{Project: nw, Actor: mira, Roles: []string{"lead"}, StartedOn: date("2026-01-01")},
		{Project: hub, Actor: carol, Roles: []string{"pm"}, StartedOn: date("2026-01-01")},
	}
	cases := []struct {
		name    string
		actor   ids.ID[ids.Actor]
		project ids.ID[ids.Project]
		at      time.Time
		want    []string
	}{
		{"before the first start", mira, hub, date("2026-01-11"), nil},
		{"first day", mira, hub, date("2026-01-12"), []string{"dev"}},
		{"last day, late in the day", mira, hub, date("2026-03-31").Add(23 * time.Hour), []string{"dev"}},
		{"between stints", mira, hub, date("2026-04-15"), nil},
		{"one open stint", mira, hub, date("2026-05-15"), []string{"dev", "reviewer"}},
		{"two open stints read as one", mira, hub, date("2026-07-01"), []string{"dev", "qa", "reviewer"}},
		{"other project", mira, nw, date("2026-07-01"), []string{"lead"}},
		{"other actor", carol, hub, date("2026-07-01"), []string{"pm"}},
		{"not a member", carol, nw, date("2026-07-01"), nil},
	}
	for _, c := range cases {
		if got := RolesAt(ms, c.actor, c.project, c.at); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// TestDescribeOnly pins the 0.1.18 default (TP-9): always allowed, roles
// filled, a Members error returned (never an allow on error), nil Members
// answers with no roles.
func TestDescribeOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mira := mustID[ids.Actor](t, ids.PrefixActor, 1)
	hub := mustID[ids.Project](t, ids.PrefixProject, 3)
	q := Question{Actor: mira, Action: "task.edit", Project: hub, At: date("2026-07-01")}

	var c Checker = DescribeOnly{Members: func(context.Context) ([]Membership, error) {
		return []Membership{{Project: hub, Actor: mira, Roles: []string{"dev"}, StartedOn: date("2026-01-01")}}, nil
	}}
	d, err := c.Check(ctx, q)
	if err != nil || !d.Allowed || !slices.Equal(d.Roles, []string{"dev"}) || d.Reason != ReasonDescribeOnly {
		t.Fatalf("member: %+v, %v", d, err)
	}

	d, err = DescribeOnly{}.Check(ctx, q)
	if err != nil || !d.Allowed || d.Roles != nil {
		t.Fatalf("nil Members: %+v, %v", d, err)
	}

	boom := errors.New("roster unreachable")
	d, err = DescribeOnly{Members: func(context.Context) ([]Membership, error) { return nil, boom }}.Check(ctx, q)
	if !errors.Is(err, boom) || d.Allowed {
		t.Fatalf("error: %+v, %v", d, err)
	}
}
