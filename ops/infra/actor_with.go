package infra

// actor_with.go — `lore actor list --with contacts,teams,…` (devco D-861).
// actor show reads one person's contacts, profile, teams, projects and
// stints; a bot that needs the whole people list with them had to call
// show once per person. --with joins the asked-for parts into every row of
// the list, reading each table once for the whole list.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
)

// The parts `actor list --with` joins into every person (the JSON keys
// they fill are on Actor: contacts, teams, profile, memberships, periods).
const (
	WithContacts = "contacts"
	WithTeams    = "teams"
	WithProfile  = "profile"
	WithProjects = "projects"
	WithPeriods  = "periods"
	// WithAll is every part above.
	WithAll = "all"
)

// ActorWithParts is the closed set --with takes, in the order the text
// list prints them (WithAll last).
func ActorWithParts() []string {
	return []string{WithProfile, WithContacts, WithTeams, WithProjects, WithPeriods, WithAll}
}

// actorParts is the parsed --with: which parts to join.
type actorParts map[string]bool

// parseWith reads --with values (comma-separated or repeated, spaces
// trimmed); an unknown part is E_INVALID_INPUT naming the set. "all"
// turns every part on.
func parseWith(vals []string) (actorParts, error) {
	parts := actorParts{}
	for _, v := range vals {
		for p := range strings.SplitSeq(v, ",") {
			p = strings.ToLower(strings.TrimSpace(p))
			if p == "" {
				continue
			}
			if !slices.Contains(ActorWithParts(), p) {
				return nil, errs.New(errs.InvalidInput, "--with "+work.Quote(p)+" is not one of "+strings.Join(ActorWithParts(), ", ")).
					WithHint("pass e.g. `lore actor list --with contacts,teams --json`")
			}
			if p == WithAll {
				for _, q := range ActorWithParts() {
					parts[q] = q != WithAll
				}
				continue
			}
			parts[p] = true
		}
	}
	return parts, nil
}

// ActorTeam is one roster team a person is on (teams.member_actor_ids).
type ActorTeam struct {
	ID     string `json:"id"`
	Handle string `json:"handle"`
	Name   string `json:"name,omitempty"`
}

// partSource is the profile tables an actor list --with needs, each read
// once for the whole list.
type partSource struct {
	contacts map[string][]row.Row
	periods  map[string][]row.Row
	members  map[string][]row.Row
	teams    map[string][]ActorTeam
	projects *projectNames
}

// loadParts reads the tables the asked-for parts need, every attached
// store (the profile rows live in the org roster store, TS-1, like
// actor show reads them).
func (o ops) loadParts(ctx context.Context, parts actorParts) (partSource, error) {
	var src partSource
	var err error
	if parts[WithContacts] {
		if src.contacts, err = o.rowsByActor(ctx, schema.TableActorContacts, schema.FieldContactActor); err != nil {
			return src, err
		}
	}
	if parts[WithPeriods] {
		if src.periods, err = o.rowsByActor(ctx, schema.TableActorPeriods, schema.FieldPeriodActor); err != nil {
			return src, err
		}
	}
	if parts[WithProjects] {
		if src.members, err = o.rowsByActor(ctx, schema.TableProjectMembers, schema.FieldMemberActor); err != nil {
			return src, err
		}
		src.projects = &projectNames{}
	}
	if parts[WithTeams] {
		if src.teams, err = o.teamsByActor(ctx); err != nil {
			return src, err
		}
	}
	return src, nil
}

// fillParts sets the asked-for parts of a from its own row r: an asked
// list is [] when the person has none (so a bot sees the key), an empty
// profile is left out.
func (o ops) fillParts(ctx context.Context, a *Actor, r row.Row, parts actorParts, src partSource) error {
	if parts[WithProfile] {
		a.Profile = profileOf(r)
	}
	if parts[WithContacts] {
		a.Contacts = contactsFrom(src.contacts[a.ID])
	}
	if parts[WithTeams] {
		a.Teams = append([]ActorTeam{}, src.teams[a.ID]...)
	}
	if parts[WithProjects] {
		m, err := o.membershipsFrom(ctx, src.members[a.ID], src.projects)
		if err != nil {
			return err
		}
		a.Memberships = m
	}
	if parts[WithPeriods] {
		p, err := periodsFrom(r, src.periods[a.ID])
		if err != nil {
			return err
		}
		a.Periods = p
	}
	return nil
}

// profileOf is the profile fields of an actor row (TEAM_PROFILES 2.1).
func profileOf(r row.Row) ActorProfile {
	return ActorProfile{Title: work.Field(r, schema.FieldActorTitle), Timezone: work.Field(r, schema.FieldActorTimezone),
		Location: work.Field(r, schema.FieldActorLocation), PhotoURL: work.Field(r, schema.FieldActorPhotoURL),
		Bio: work.Field(r, schema.FieldActorBio), Birthday: work.Field(r, schema.FieldActorBirthday)}
}

// teamsByActor is the active roster teams of every attached store by
// member actor id, each person's by handle. A removed team (active
// false) is left out: nobody is on it any more.
func (o ops) teamsByActor(ctx context.Context) (map[string][]ActorTeam, error) {
	rows, err := work.ListAll(ctx, o.env, schema.TableTeams, "")
	if err != nil {
		return nil, err
	}
	out := map[string][]ActorTeam{}
	seen := map[string]bool{}
	for _, s := range rows {
		id := s.Row.ID.String()
		if seen[id] {
			continue
		}
		seen[id] = true
		if work.Field(s.Row, fieldActive) == "false" {
			continue
		}
		t := ActorTeam{ID: id, Handle: "@" + strings.TrimPrefix(work.Field(s.Row, fieldHandle), "@"), Name: work.Field(s.Row, fieldDisplayName)}
		for _, m := range value.ItemStrings(s.Row.Fields[schema.FieldTeamMembers]) {
			if !slices.ContainsFunc(out[m], func(x ActorTeam) bool { return x.ID == t.ID }) {
				out[m] = append(out[m], t)
			}
		}
	}
	for k := range out {
		slices.SortFunc(out[k], func(x, y ActorTeam) int { return strings.Compare(x.Handle, y.Handle) })
	}
	return out, nil
}

// partLines is the indented lines the text list prints under a person
// for the asked-for parts (none when nothing was asked; "-" for an asked
// part the person has none of, so a missing line never reads as "not
// asked").
func partLines(a Actor, parts actorParts) []string {
	var out []string
	add := func(label, text string) {
		if text == "" {
			text = "-"
		}
		out = append(out, fmt.Sprintf("    %-9s %s", label+":", text))
	}
	if parts[WithProfile] {
		var kv []string
		for _, f := range [][2]string{{"title", a.Profile.Title}, {"timezone", a.Profile.Timezone}, {"location", a.Profile.Location},
			{"photo_url", a.Profile.PhotoURL}, {"birthday", a.Profile.Birthday}} {
			if f[1] != "" {
				kv = append(kv, f[0]+" "+f[1])
			}
		}
		add("profile", strings.Join(kv, " · "))
	}
	if parts[WithContacts] {
		var cs []string
		for _, c := range a.Contacts {
			s := c.Type + " " + c.Value
			if c.Label != "" {
				s += " (" + c.Label + ")"
			}
			cs = append(cs, s)
		}
		add("contacts", strings.Join(cs, ", "))
	}
	if parts[WithTeams] {
		var ts []string
		for _, t := range a.Teams {
			ts = append(ts, t.Handle)
		}
		add("teams", strings.Join(ts, ", "))
	}
	if parts[WithProjects] {
		var ps []string
		for _, m := range a.Memberships {
			if m.EndedOn != "" {
				continue // the list shows current projects; actor show has the ended ones
			}
			s := m.Project
			if len(m.Roles) > 0 {
				s += " (" + strings.Join(m.Roles, ", ") + ")"
			}
			ps = append(ps, s)
		}
		add("projects", strings.Join(ps, ", "))
	}
	if parts[WithPeriods] {
		var ps []string
		for _, p := range a.Periods {
			ps = append(ps, p.StartedOn+" → "+orNow(p.EndedOn)+" "+p.Type)
		}
		add("periods", strings.Join(ps, ", "))
	}
	return out
}
