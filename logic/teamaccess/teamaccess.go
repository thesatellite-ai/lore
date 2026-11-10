// Package teamaccess is the access-control seam of team profiles
// (TEAM_PROFILES_DESIGN 10, TP-9): the one question "may Actor do Action
// on Project at At?" as an interface, read from the same project roles the
// memberships hold (TP-8, TP-10).
//
// Why a seam now, with nothing calling it: in 0.1.18 roles only describe;
// access control comes with the server. Callers that will ask the question
// (the server, a future op guard) depend on Checker, never on a concrete
// model, so a team on simple roles, RBAC, ABAC, relationship-based checks
// (OpenFGA, SpiceDB) or a policy engine (Casbin, OPA, Cedar) plugs its own
// Checker in through one typed option. DescribeOnly is the 0.1.18 default:
// it allows everything and only fills Decision.Roles.
//
// Pure: no I/O and no clock. The memberships come from the caller's
// Members function and the time from Question.At.
package teamaccess

import (
	"context"
	"slices"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
)

// Question is "may Actor do Action on Project at At?".
type Question struct {
	Actor ids.ID[ids.Actor]
	// Action is an op name ("task.edit") or a server action.
	Action  string
	Project ids.ID[ids.Project]
	At      time.Time
}

// Decision is the answer; Roles are the actor's roles on the project at
// At (empty when the actor is not a member then).
type Decision struct {
	Allowed bool
	Reason  string
	Roles   []string
}

// Checker answers questions; the server picks the implementation (roles,
// RBAC, a policy engine) with one typed option.
type Checker interface {
	Check(ctx context.Context, q Question) (Decision, error)
}

// Membership is the part of a project_members row the seam reads.
type Membership struct {
	Project   ids.ID[ids.Project]
	Actor     ids.ID[ids.Actor]
	Roles     []string
	StartedOn time.Time
	// EndedOn is the last day of the stint; zero = open.
	EndedOn time.Time
}

// ActiveOn reports whether the stint covers day t: started_on <= t, and
// t <= ended_on when it has ended. Dates are compared by calendar day
// (ended_on is the LAST day of the stint, TP-12), so a time later on the
// ended_on day still counts.
func (m Membership) ActiveOn(t time.Time) bool {
	d := day(t)
	if d.Before(day(m.StartedOn)) {
		return false
	}
	return m.EndedOn.IsZero() || !d.After(day(m.EndedOn))
}

// RolesAt returns the roles of actor on project at t: the union of the
// roles of every stint active on t's day (two open stints after a merge
// read as one, TEAM_PROFILES 2.6), sorted, without duplicates. nil when
// the actor is not a member then.
func RolesAt(ms []Membership, actor ids.ID[ids.Actor], project ids.ID[ids.Project], t time.Time) []string {
	var out []string
	for _, m := range ms {
		if m.Actor != actor || m.Project != project || !m.ActiveOn(t) {
			continue
		}
		out = append(out, m.Roles...)
	}
	if len(out) == 0 {
		return nil
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Reasons DescribeOnly gives.
const (
	// ReasonDescribeOnly: 0.1.18 roles describe, they do not grant (TP-9).
	ReasonDescribeOnly = "roles describe only; every action is allowed (TP-9)"
)

// DescribeOnly is the 0.1.18 default: every question is allowed and
// Decision.Roles is filled (TP-9: roles describe, they do not grant).
// Members reads the memberships (the org roster's project_members rows);
// a nil Members answers with no roles.
type DescribeOnly struct {
	Members func(ctx context.Context) ([]Membership, error)
}

var _ Checker = DescribeOnly{}

// Check implements Checker. A Members error is returned as is (fail
// closed: the caller decides, nothing is allowed on an error).
func (d DescribeOnly) Check(ctx context.Context, q Question) (Decision, error) {
	var ms []Membership
	if d.Members != nil {
		var err error
		if ms, err = d.Members(ctx); err != nil {
			return Decision{}, err
		}
	}
	return Decision{Allowed: true, Reason: ReasonDescribeOnly, Roles: RolesAt(ms, q.Actor, q.Project, q.At)}, nil
}

// day is t's calendar day as midnight UTC (the same rule as
// teamprofile.Day, repeated so this seam imports nothing but model).
func day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
