package tui

// Profile block of an actor's row screen (TEAM_PROFILES_DESIGN 9, TP-23):
// read-only, appended under the actor's own fields by one dispatch line in
// show. It reads actor_contacts, actor_periods and project_members through
// Source.List over every attached store (no new Source method, nothing
// written). Values are shown plain (TP-6): none of these fields is
// Sensitive, and the generic masking of fieldText is left untouched.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Profile block words.
const (
	// dateOpen stands for an empty end date (the open period / current member).
	dateOpen = "now"
	// boolTrue is the text form of a true bool field (a typed Bool and a row
	// file's plain JSON inferred as text read the same).
	boolTrue = "true"
	// bioWidth bounds the one-line bio of the profile section.
	bioWidth = 70
	// projectFieldName is projects.name (schema's own constant is unexported).
	projectFieldName = "name"
)

// profile prints the profile block of actor h: the profile fields (status
// read with teamprofile.StatusOf, so a v0.1.17 row shows left/active), the
// contacts, the company periods (with the TP-22 synthesised one for a
// migrated actor) and the project memberships, current first. A read error
// is printed in place of the block: the row screen above it stays useful.
func (b *browser) profile(h hit) {
	b.put("\n  profile\n")
	if err := b.profileBody(h); err != nil {
		b.put(fmt.Sprintf("    (profile not available: %v)\n", err))
	}
}

// profileBody reads every section's rows, then renders them.
func (b *browser) profileBody(h hit) error {
	actor := h.r.ID.String()
	contacts, err := b.rowsOf(schema.TableActorContacts, schema.FieldContactActor, actor)
	if err != nil {
		return err
	}
	periodRows, err := b.rowsOf(schema.TableActorPeriods, schema.FieldPeriodActor, actor)
	if err != nil {
		return err
	}
	periods, err := teamprofile.PeriodsOf(h.r, periodRows)
	if err != nil {
		return err
	}
	members, err := b.rowsOf(schema.TableProjectMembers, schema.FieldMemberActor, actor)
	if err != nil {
		return err
	}
	projects, err := b.projectNames(members)
	if err != nil {
		return err
	}
	b.put(renderProfile(h.r, contacts, periods, members, projects))
	return nil
}

// rowsOf is the live rows of table whose field is id, in every store.
func (b *browser) rowsOf(table, field, id string) ([]row.Row, error) {
	hits, err := b.rows(row.Table(table))
	if err != nil {
		return nil, err
	}
	var out []row.Row
	for _, x := range hits {
		if txt(x.r, field) == id {
			out = append(out, x.r)
		}
	}
	return out, nil
}

// projectNames maps each project id the memberships name to its name; read
// only when there is a membership. A project not attached here (the
// relation allows dangling, TEAM_PROFILES 2.6) is simply absent: its id is
// shown instead.
func (b *browser) projectNames(members []row.Row) (map[string]string, error) {
	names := map[string]string{}
	if len(members) == 0 {
		return names, nil
	}
	hits, err := b.rows(schema.TableProjects)
	if err != nil {
		return nil, err
	}
	for _, x := range hits {
		if n := txt(x.r, projectFieldName); n != "" {
			names[x.r.ID.String()] = n
		}
	}
	return names, nil
}

// renderProfile is the block's text below its "profile" heading; pure, so
// the layout is tested without a Source.
func renderProfile(actor row.Row, contacts []row.Row, periods []teamprofile.Period, members []row.Row, projects map[string]string) string {
	var s strings.Builder

	// Profile fields: status always (it has a read-time default), the rest
	// only when set.
	status := teamprofile.StatusOf(actor.Fields)
	if status == schema.ActorStatusOnLeave {
		if from, until := txt(actor, schema.FieldActorLeaveFrom), txt(actor, schema.FieldActorLeaveUntil); from != "" || until != "" {
			status += fmt.Sprintf(" (%s – %s)", orDash(from), orDash(until))
		}
	}
	fmt.Fprintf(&s, "    %-10s %s\n", "status", status)
	set := 0
	for _, f := range []string{schema.FieldActorTitle, schema.FieldActorLocation, schema.FieldActorTimezone,
		schema.FieldActorBirthday, schema.FieldActorPhotoURL, schema.FieldActorBio} {
		v := strings.Join(strings.Fields(txt(actor, f)), " ")
		if v == "" {
			continue
		}
		if f == schema.FieldActorBio {
			v = clip(v, bioWidth)
		}
		fmt.Fprintf(&s, "    %-10s %s\n", f, v)
		set++
	}
	if set == 0 {
		fmt.Fprintf(&s, "    (no profile fields set)\n")
	}

	// Contacts: by type, primary first, then value.
	fmt.Fprintf(&s, "  contacts (%d)\n", len(contacts))
	if len(contacts) == 0 {
		fmt.Fprintf(&s, "    (none)\n")
	}
	contacts = slices.Clone(contacts)
	slices.SortFunc(contacts, func(x, y row.Row) int {
		if c := strings.Compare(txt(x, schema.FieldContactType), txt(y, schema.FieldContactType)); c != 0 {
			return c
		}
		if px, py := isTrue(x, schema.FieldContactPrimary), isTrue(y, schema.FieldContactPrimary); px != py {
			if px {
				return -1
			}
			return 1
		}
		return strings.Compare(txt(x, schema.FieldContactValue), txt(y, schema.FieldContactValue))
	})
	for _, c := range contacts {
		var flags []string
		if l := txt(c, schema.FieldContactLabel); l != "" {
			flags = append(flags, l)
		}
		if isTrue(c, schema.FieldContactPrimary) {
			flags = append(flags, schema.FieldContactPrimary)
		}
		if isTrue(c, schema.FieldContactIdentity) {
			flags = append(flags, schema.FieldContactIdentity)
		}
		fmt.Fprintf(&s, "    %-10s %s", txt(c, schema.FieldContactType), txt(c, schema.FieldContactValue))
		if len(flags) > 0 {
			fmt.Fprintf(&s, "  [%s]", strings.Join(flags, ", "))
		}
		if n := txt(c, schema.FieldContactNote); n != "" {
			fmt.Fprintf(&s, "  %s", clip(strings.Join(strings.Fields(n), " "), labelWidth))
		}
		fmt.Fprintf(&s, "\n")
	}

	// Company periods, oldest first (PeriodsOf's order).
	fmt.Fprintf(&s, "  company periods (%d)\n", len(periods))
	if len(periods) == 0 {
		fmt.Fprintf(&s, "    (none)\n")
	}
	for _, p := range periods {
		end := dateOpen
		if !p.Open() {
			end = p.EndedOn.Format(value.DateLayout)
		}
		fmt.Fprintf(&s, "    %-14s %s – %s", p.Type, p.StartedOn.Format(value.DateLayout), end)
		if p.OrgRole != "" {
			fmt.Fprintf(&s, "  %s", p.OrgRole)
		}
		if p.EndReason != "" {
			fmt.Fprintf(&s, "  ended: %s", p.EndReason)
		}
		if p.Synthesised() {
			fmt.Fprintf(&s, "  (from active/left_at, no period row yet)")
		} else if p.Source == schema.PeriodSourceMigrated {
			fmt.Fprintf(&s, "  (%s)", schema.PeriodSourceMigrated)
		}
		fmt.Fprintf(&s, "\n")
	}

	// Project memberships: current, then ended; each by start day.
	fmt.Fprintf(&s, "  projects (%d)\n", len(members))
	if len(members) == 0 {
		fmt.Fprintf(&s, "    (none)\n")
	}
	members = slices.Clone(members)
	slices.SortFunc(members, func(x, y row.Row) int {
		cx, cy := txt(x, schema.FieldMemberEndedOn) == "", txt(y, schema.FieldMemberEndedOn) == ""
		if cx != cy {
			if cx {
				return -1
			}
			return 1
		}
		return strings.Compare(txt(x, schema.FieldMemberStartedOn), txt(y, schema.FieldMemberStartedOn))
	})
	for _, m := range members {
		pid := txt(m, schema.FieldMemberProject)
		name := pid
		if n, ok := projects[pid]; ok {
			name = n + " (" + pid + ")"
		}
		state, end := "current", dateOpen
		if e := txt(m, schema.FieldMemberEndedOn); e != "" {
			state, end = "ended", e
		}
		fmt.Fprintf(&s, "    %-7s %s  %s  %s – %s", state, name, strings.Join(value.ItemStrings(m.Fields[schema.FieldMemberRoles]), ", "),
			txt(m, schema.FieldMemberStartedOn), end)
		if r := txt(m, schema.FieldMemberEndReason); r != "" {
			fmt.Fprintf(&s, "  ended: %s", r)
		}
		fmt.Fprintf(&s, "\n")
	}
	return s.String()
}

// txt is the text of field name of r, "" when absent or null.
func txt(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// isTrue reports whether the bool field name of r is true.
func isTrue(r row.Row, name string) bool { return txt(r, name) == boolTrue }

// orDash is s, or "?" for an unknown leave bound.
func orDash(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

// clip bounds s to n runes with an ellipsis.
func clip(s string, n int) string {
	if rs := []rune(s); len(rs) > n {
		return string(rs[:n-1]) + "…"
	}
	return s
}
