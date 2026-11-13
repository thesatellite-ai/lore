package infra

import (
	"context"
	"fmt"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/actorsource"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// ActorListIn is the input of actor.list.
type ActorListIn struct {
	Store string `json:"store,omitempty" flag:"store" alias:"to" desc:"only actors of this store (default every attached store)"`
	// Status keeps one status (TEAM_PROFILES 2.7, TP-15).
	Status string `json:"status,omitempty" flag:"status" desc:"only actors with this status: active, on_leave, left or archived"`
	// Archived lists archived people too; they are out of the default list
	// (TP-15).
	Archived bool `json:"archived,omitempty" flag:"archived" desc:"include archived people (left out by default)"`
	// --created-by / --updated-by: the shared writer filters of every
	// noun list (INTEG-AUDIT-1; engine.List applies them).
	metalist.Audit
}

// Actor is one actor in v0.1.17's --json form (the ent Actor struct; the
// machine-local last_seen_at no longer travels in a row), plus the team
// profile keys of TEAM_PROFILES 3.1 (added, none renamed). The profile
// lists are filled by actor show only.
type Actor struct {
	ID          string          `json:"id"`
	CreatedAt   string          `json:"created_at,omitempty"`
	UpdatedAt   string          `json:"updated_at,omitempty"`
	Kind        string          `json:"kind"`
	DisplayName string          `json:"display_name"`
	StableKey   string          `json:"stable_key"`
	Handle      string          `json:"handle,omitempty"`
	Label       string          `json:"label,omitempty"`
	OwnerID     string          `json:"owner_actor_id,omitempty"`
	Active      bool            `json:"active"`
	LeftAt      string          `json:"left_at,omitempty"`
	Store       store.SourceRef `json:"_source,omitempty,omitzero"`
	// Status is teamprofile.StatusOf (an empty status reads from active).
	Status     string `json:"status"`
	LeaveFrom  string `json:"leave_from,omitempty"`
	LeaveUntil string `json:"leave_until,omitempty"`
	// StatusNote is why the status is what it is (actor status --note;
	// "" for a row that has none).
	StatusNote  string           `json:"status_note,omitempty"`
	Profile     ActorProfile     `json:"profile,omitzero"`
	Contacts    []ActorContact   `json:"contacts,omitempty"`
	Periods     []ActorPeriod    `json:"periods,omitempty"`
	Memberships []ActorMemberOut `json:"memberships,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
	// Stores are the stores holding a copy of this actor (list only): one
	// actor is written into every store its person writes to under the
	// same id, and the list shows it once (FB-ACTOR-STORES).
	Stores []string `json:"stores,omitempty"`
	// SourceNote marks a name read from a copy because the source could
	// not be read (actor_source.go); Source and Copies are actor show's
	// source of truth and every store holding a copy.
	SourceNote string         `json:"source_note,omitempty"`
	Source     ActorSourceOut `json:"source,omitzero"`
	Copies     []ActorCopyOut `json:"copies,omitempty"`
}

// ActorProfile is the profile fields of TEAM_PROFILES 2.1 (TP-5).
type ActorProfile struct {
	Title    string `json:"title,omitempty"`
	Timezone string `json:"timezone,omitempty"`
	Location string `json:"location,omitempty"`
	PhotoURL string `json:"photo_url,omitempty"`
	Bio      string `json:"bio,omitempty"`
	Birthday string `json:"birthday,omitempty"`
}

// ActorContact is one contact of the person (TEAM_PROFILES 2.2), plain
// (TP-6).
type ActorContact struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Value    string `json:"value"`
	Label    string `json:"label,omitempty"`
	Primary  bool   `json:"primary"`
	Identity bool   `json:"identity"`
}

// ActorPeriod is one company stint (TEAM_PROFILES 2.5); the read-time
// period of TP-22 has no id and source "migrated".
type ActorPeriod struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	StartedOn string `json:"started_on"`
	EndedOn   string `json:"ended_on,omitempty"`
	OrgRole   string `json:"org_role,omitempty"`
	EndReason string `json:"end_reason,omitempty"`
	Source    string `json:"source,omitempty"`
}

// ActorMemberOut is one project membership (TEAM_PROFILES 2.6): open ones
// first, then ended ones.
type ActorMemberOut struct {
	ID        string   `json:"id"`
	ProjectID string   `json:"project_id"`
	Project   string   `json:"project"`
	Roles     []string `json:"roles"`
	StartedOn string   `json:"started_on"`
	EndedOn   string   `json:"ended_on,omitempty"`
	EndReason string   `json:"end_reason,omitempty"`
}

// ActorListOut is v0.1.17's list envelope.
type ActorListOut struct {
	SchemaVersion int     `json:"schema_version"`
	Kind          string  `json:"kind"`
	Count         int     `json:"count,omitempty"`
	Data          []Actor `json:"data"`
}

// Text is v0.1.17's list: "<id> [<kind>] <display name>", with the status
// after the name when it is not active (TP-15: on_leave and left are
// marked), then the stores holding the actor in a column, and a count
// line naming how many stores the list was merged from (FB-ACTOR-STORES:
// one actor in the repo and in me is listed once, so without the column
// a store looked missing).
func (o ActorListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no actors)\n"
	}
	heads := make([]string, len(o.Data))
	stores := make([][]string, len(o.Data))
	for i, a := range o.Data {
		heads[i] = fmt.Sprintf("%s [%s] %s%s%s", a.ID, a.Kind, a.DisplayName, statusMark(a), noteMark(a.SourceNote))
		stores[i] = a.Stores
	}
	return storeColumn(heads, stores, "actor", "actors")
}

// noteMark is "  <note>", or "" for none.
func noteMark(note string) string {
	if note == "" {
		return ""
	}
	return "  " + note
}

// statusMark is "" for active, else "  (left)", "  (on_leave until …)",
// with the status note after a colon: "  (on_leave until …: parental
// leave)".
func statusMark(a Actor) string {
	if a.Status == "" || a.Status == schema.ActorStatusActive {
		return ""
	}
	s := a.Status
	if a.Status == schema.ActorStatusOnLeave && a.LeaveUntil != "" {
		s += " until " + a.LeaveUntil
	}
	if a.StatusNote != "" {
		s += ": " + a.StatusNote
	}
	return "  (" + s + ")"
}

// ActorShowOut is v0.1.17's show envelope.
type ActorShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          Actor  `json:"data"`
	// Today is the day the leave note compares against ("" omits it).
	Today string `json:"-"`
}

// Text is v0.1.17's show block (last_seen is machine-local and gone), then
// the team profile lines of TEAM_PROFILES 3.1 when the actor has any.
// Text is the show text ending with the writers line (INTEG-AUDIT-2).
func (o ActorShowOut) Text() string {
	d := o.Data
	return o.mainText() + auditrec.Line(d.CreatedAt, d.CreatedBy, d.UpdatedAt, d.UpdatedBy)
}

func (o ActorShowOut) mainText() string {
	a := o.Data
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n  kind:        %s\n  display:     %s%s\n  stable_key:  %s\n", a.ID, a.Kind, a.DisplayName, noteMark(a.SourceNote), a.StableKey)
	if a.Status != "" && a.Status != schema.ActorStatusActive {
		fmt.Fprintf(&b, "  status:      %s\n", a.Status)
	}
	if a.Status == schema.ActorStatusOnLeave {
		fmt.Fprintf(&b, "  leave:       %s → %s\n", a.LeaveFrom, orNow(a.LeaveUntil))
		// 5.3: a passed leave_until never flips the status on a read.
		if a.LeaveUntil != "" && o.Today != "" && a.LeaveUntil < o.Today {
			fmt.Fprintf(&b, "               leave ended %s; still on_leave (lore actor status --actor %s --to active)\n", a.LeaveUntil, a.ID)
		}
	}
	if a.StatusNote != "" {
		fmt.Fprintf(&b, "  note:        %s\n", a.StatusNote)
	}
	for _, kv := range [][2]string{{"title", a.Profile.Title}, {"timezone", a.Profile.Timezone}, {"location", a.Profile.Location},
		{"photo_url", a.Profile.PhotoURL}, {"birthday", a.Profile.Birthday}} {
		if kv[1] != "" {
			fmt.Fprintf(&b, "  %-12s %s\n", kv[0]+":", kv[1])
		}
	}
	lead := "  contacts:    "
	for _, c := range a.Contacts {
		flags := []string{}
		if c.Primary {
			flags = append(flags, "primary")
		}
		if c.Identity {
			flags = append(flags, "identity")
		}
		fmt.Fprintf(&b, "%s%-8s %s  %s  %s\n", lead, c.Type, c.Value, c.Label, strings.Join(flags, " "))
		lead = "               "
	}
	lead = "  periods:     "
	periods := a.Periods
	if len(periods) == 1 && periods[0].ID == "" {
		// A lone read-time period (TP-22) only restates created_at and
		// active/left_at, which every v0.1.17 actor has: the text block
		// stays v0.1.17's for them; --json and period list show it.
		periods = nil
	}
	for _, p := range periods {
		line := p.StartedOn + " → " + orNow(p.EndedOn) + "  " + p.Type
		if p.OrgRole != "" {
			line += "  " + p.OrgRole
		}
		if p.ID == "" && p.Source == schema.PeriodSourceMigrated {
			line += "  (from the roster: active/left_at)"
		}
		fmt.Fprintf(&b, "%s%s\n", lead, line)
		lead = "               "
	}
	lead = "  projects:    "
	for _, m := range a.Memberships {
		when := "since " + m.StartedOn
		if m.EndedOn != "" {
			when = m.StartedOn + " → " + m.EndedOn
		}
		fmt.Fprintf(&b, "%s%s  %s  %s\n", lead, m.Project, strings.Join(m.Roles, ", "), when)
		lead = "               "
	}
	b.WriteString(sourceText(a)) // the source and every copy (actor_source.go)
	return b.String()
}

// orNow is s, or "now" when empty (an open end).
func orNow(s string) string {
	if s == "" {
		return "now"
	}
	return s
}

// actorOf converts a row of src into its v0.1.17 record plus status.
func actorOf(lab work.Labeler, s work.Sourced) Actor {
	r := s.Row
	return Actor{
		ID: r.ID.String(), CreatedAt: timeOf(r, true), UpdatedAt: timeOf(r, false), CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy,
		Kind: work.Field(r, fieldKind), DisplayName: work.Field(r, fieldDisplayName), StableKey: work.Field(r, fieldStableKey),
		Handle: work.Field(r, fieldHandle), Label: work.Field(r, fieldLabel), OwnerID: work.Field(r, fieldOwner),
		Active: activeOf(r), LeftAt: work.TimeText(r, fieldLeftAt), Store: sourceOf(lab, s.Source),
		Status: teamprofile.StatusOf(r.Fields), LeaveFrom: work.Field(r, schema.FieldActorLeaveFrom), LeaveUntil: work.Field(r, schema.FieldActorLeaveUntil),
		StatusNote: work.Field(r, schema.FieldActorStatusNote),
	}
}

// activeOf is the active flag; a row without one is active (v0.1.17's
// ent default true).
func activeOf(r row.Row) bool { return work.Field(r, fieldActive) != "false" }

// actorList lists the live actors of every attached store, oldest first
// (v0.1.17 ordered by created_at); one actor seen in several stores (same
// deterministic id) is listed once, from the first store. Archived people
// are left out unless --archived or --status archived (TP-15).
func (o ops) actorList(ctx context.Context, in ActorListIn) (ActorListOut, error) {
	if in.Status != "" && !slices.Contains(schema.ActorStatuses(), in.Status) {
		return ActorListOut{}, errs.New(errs.InvalidInput, "--status "+work.Quote(in.Status)+" is not one of "+strings.Join(schema.ActorStatuses(), ", "))
	}
	rows, err := work.ListAll(ctx, o.env, schema.TableActors, in.Store)
	if err != nil {
		return ActorListOut{}, err
	}
	out := ActorListOut{SchemaVersion: SchemaVersion, Kind: KindActorList, Data: []Actor{}}
	held := storesOf(rows)
	seen := map[string]bool{}
	for _, s := range rosterFirst(sortByCreated(rows), o.rosterSource(ctx, in.Store)) {
		if seen[s.Row.ID.String()] {
			continue
		}
		seen[s.Row.ID.String()] = true
		a := actorOf(work.LabelerOf(o.env), s)
		a.Stores = held[s.Row.ID.String()]
		// FB-ACTOR-KEY: the copy shown may lack the stable key and handle
		// by design (a repo that is not an org's own is written the F18
		// projection: its files are committed, and an email there is
		// published with the code); another store's copy of the same
		// actor (me, an org store) holds them, so the merged view shows
		// them. Nothing is written into the repo.
		fillKey(&a, rows)
		o.applySource(ctx, &a, false, in.Store != "") // the source's name (actor_source.go)
		if in.Status != "" && a.Status != in.Status {
			continue
		}
		if in.Status == "" && !in.Archived && a.Status == schema.ActorStatusArchived {
			continue
		}
		out.Data = append(out.Data, a)
	}
	out.Count = len(out.Data)
	return out, nil
}

// ActorShowIn is the input of actor.show.
type ActorShowIn struct {
	ID string `json:"id" arg:"0" desc:"the actor: act_ id, @handle or stable key (human:mira@acme.com)"`
	// Store picks one store's copy of the actor row.
	Store string `json:"store,omitempty" flag:"store" alias:"to" desc:"show this store's copy of the actor (default: the first attached store that has it, the repo first)"`
}

// actorShow shows one actor (E_NOT_FOUND "actor "<ref>" not found"). An
// actor row is written into every store the person writes to under the
// same deterministic id (the actor row once per store), so the same id in
// the repo and the personal store `me` is one actor, not an ambiguity:
// without --store the first copy in store precedence order (the repo
// first) is shown; --store names one copy. The team profile (contacts,
// periods, memberships) is read from every attached store: its rows live
// only in the org roster store (TS-1).
func (o ops) actorShow(ctx context.Context, in ActorShowIn) (ActorShowOut, error) {
	id, err := o.actorRef(ctx, strings.TrimSpace(in.ID))
	if err != nil {
		return ActorShowOut{}, err
	}
	notFound := errs.New(errs.NotFound, "actor "+work.Quote(in.ID)+" not found").WithHint("use `lore actor list` to see available actor IDs")
	if id.IsZero() {
		return ActorShowOut{}, notFound
	}
	if in.Store == "" {
		// The org roster's copy is the person's profile (TS-1); the repo
		// keeps only {id, display_name, kind}, written once when they first
		// wrote there, so it wins only where no roster holds the actor.
		if rs := o.rosterSource(ctx, ""); rs != "" {
			if copies, lerr := work.ListAll(ctx, o.env, schema.TableActors, string(rs)); lerr == nil {
				for _, s := range copies {
					if s.Row.ID == id {
						return o.showOut(ctx, s, in.Store != "")
					}
				}
			}
		}
		src, r, gerr := work.Get(ctx, o.env, NounActor, schema.TableActors, id)
		if gerr == nil {
			return o.showOut(ctx, work.Sourced{Source: src, Row: r}, in.Store != "")
		}
		if !errs.Has(gerr, errs.AmbiguousID) {
			return ActorShowOut{}, gerr
		}
	}
	rows, err := work.ListAll(ctx, o.env, schema.TableActors, in.Store)
	if err != nil {
		return ActorShowOut{}, err
	}
	for _, s := range rows {
		if s.Row.ID == id {
			return o.showOut(ctx, s, in.Store != "")
		}
	}
	if in.Store == "" {
		return ActorShowOut{}, notFound
	}
	return ActorShowOut{}, errs.New(errs.NotFound, "actor "+work.Quote(in.ID)+" not found in store "+work.Quote(in.Store)).
		WithHint("use `lore actor list --store " + in.Store + "` to see that store's actors")
}

// rosterSource is the attached store holding the org roster ("" when the
// Env has no roster seam, this repo has no org, the roster cannot be read
// here, or only names another store than only). Why: an actor row exists
// once per store under one id (the actor row once per store), and only
// the roster's copy carries the stable key and the profile; a read that
// fails is no roster, never an error, so show and list keep working.
func (o ops) rosterSource(ctx context.Context, only string) store.Source {
	ps, ok := upgrade.As[profilestore.Store](o.env)
	if !ok {
		return ""
	}
	r, done, err := ps.Roster(ctx, false)
	defer done()
	if err != nil || r.Source() == store.SourceRepo {
		return ""
	}
	if only != "" && store.Source(only) != r.Source() {
		return ""
	}
	if !slices.Contains(o.env.Stores(), r.Source()) {
		return ""
	}
	return r.Source()
}

// rosterFirst moves every row of the roster store rs ahead of the other
// copies of the same actor, keeping the order otherwise: the list shows
// one row per id, the first one seen, so the roster's profile wins over
// the repo's {id, display_name, kind} copy.
func rosterFirst(rows []work.Sourced, rs store.Source) []work.Sourced {
	if rs == "" {
		return rows
	}
	inRoster := map[ids.AnyID]bool{}
	for _, s := range rows {
		if s.Source == rs {
			inRoster[s.Row.ID] = true
		}
	}
	out := make([]work.Sourced, 0, len(rows))
	for _, s := range rows {
		if inRoster[s.Row.ID] && s.Source != rs {
			continue
		}
		out = append(out, s)
	}
	return out
}

// actorRef resolves an actor reference to an id: an act_ id as is; a
// @handle or a stable key against the attached stores' actors (between
// two people holding one handle, the pickable one wins); else, when the
// Env offers the roster seam, its ResolveActor (the contact forms,
// email:… and github:…, TEAM_PROFILES 6). The zero id is "not found";
// E_AMBIGUOUS_ID names the candidates.
func (o ops) actorRef(ctx context.Context, ref string) (ids.AnyID, error) {
	if id, err := ids.ParseAny(ref); err == nil {
		if id.Prefix() != ids.PrefixActor {
			return ids.AnyID{}, nil
		}
		return id, nil
	}
	if ref == "" || strings.HasPrefix(ref, string(ids.PrefixActor)+"_") {
		return ids.AnyID{}, nil
	}
	rows, err := work.ListAll(ctx, o.env, schema.TableActors, "")
	if err != nil {
		return ids.AnyID{}, err
	}
	handle, isHandle := strings.CutPrefix(ref, handleMark)
	var hits []row.Row
	for _, s := range rows {
		// an old key (key_aliases, after an email change) names the same
		// actor as its current stable_key (SOURCE_OF_TRUTH)
		match := work.Field(s.Row, fieldStableKey) == ref || slices.Contains(actorsource.SplitAliases(work.Field(s.Row, schema.FieldActorKeyAliases)), ref)
		if isHandle {
			match = work.Field(s.Row, fieldHandle) == handle
		}
		if match && !slices.ContainsFunc(hits, func(h row.Row) bool { return h.ID == s.Row.ID }) {
			hits = append(hits, s.Row)
		}
	}
	if len(hits) > 1 {
		var pick []row.Row
		for _, h := range hits {
			if teamprofile.Pickable(teamprofile.StatusOf(h.Fields)) {
				pick = append(pick, h)
			}
		}
		if len(pick) == 1 {
			hits = pick
		}
	}
	switch len(hits) {
	case 1:
		return hits[0].ID, nil
	case 0:
		return o.rosterRef(ctx, ref)
	}
	names := make([]string, 0, len(hits))
	for _, h := range hits {
		names = append(names, h.ID.String()+" ("+work.Field(h, fieldDisplayName)+")")
	}
	return ids.AnyID{}, errs.New(errs.AmbiguousID, "actor "+ref+" matches "+strings.Join(names, ", ")).WithHint("name one by its act_ id")
}

// rosterRef asks the roster seam (an Env upgrade) for a reference no
// attached actor row matched; any failure there is "not found" here.
func (o ops) rosterRef(ctx context.Context, ref string) (ids.AnyID, error) {
	ps, ok := upgrade.As[profilestore.Store](o.env)
	if !ok {
		return ids.AnyID{}, nil
	}
	r, done, err := ps.Roster(ctx, false)
	defer done()
	if err != nil {
		return ids.AnyID{}, nil // no roster here (no org, a client repo): not found
	}
	a, err := r.ResolveActor(ctx, ref)
	if errs.Has(err, errs.AmbiguousID) {
		return ids.AnyID{}, err
	}
	if err != nil {
		return ids.AnyID{}, nil
	}
	return a.ID, nil
}

// showOut is the show envelope of s with its team profile. pinned is a
// show of one store's copy (--store): that copy is shown as its file has it.
func (o ops) showOut(ctx context.Context, s work.Sourced, pinned bool) (ActorShowOut, error) {
	a := actorOf(work.LabelerOf(o.env), s)
	o.applySource(ctx, &a, true, pinned) // the source and every copy (actor_source.go)
	r := s.Row
	a.Profile = ActorProfile{Title: work.Field(r, schema.FieldActorTitle), Timezone: work.Field(r, schema.FieldActorTimezone),
		Location: work.Field(r, schema.FieldActorLocation), PhotoURL: work.Field(r, schema.FieldActorPhotoURL),
		Bio: work.Field(r, schema.FieldActorBio), Birthday: work.Field(r, schema.FieldActorBirthday)}
	var err error
	if a.Contacts, err = o.contactsOf(ctx, r.ID); err != nil {
		return ActorShowOut{}, err
	}
	if a.Periods, err = o.periodsOf(ctx, r); err != nil {
		return ActorShowOut{}, err
	}
	if a.Memberships, err = o.membershipsOf(ctx, r.ID); err != nil {
		return ActorShowOut{}, err
	}
	return ActorShowOut{SchemaVersion: SchemaVersion, Kind: KindActorShow, Data: a, Today: opclock.Now(o.env).UTC().Format(value.DateLayout)}, nil
}

// profileRows lists the live rows of a profile table in every attached
// store that belong to actor (field), once per id.
func (o ops) profileRows(ctx context.Context, table row.Table, field string, actor ids.AnyID) ([]row.Row, error) {
	all, err := work.ListAll(ctx, o.env, table, "")
	if err != nil {
		return nil, err
	}
	var out []row.Row
	seen := map[ids.AnyID]bool{}
	for _, s := range all {
		if work.Field(s.Row, field) == actor.String() && !seen[s.Row.ID] {
			seen[s.Row.ID] = true
			out = append(out, s.Row)
		}
	}
	return out, nil
}

// contactsOf is actor's contacts, by type then value.
func (o ops) contactsOf(ctx context.Context, actor ids.AnyID) ([]ActorContact, error) {
	rows, err := o.profileRows(ctx, schema.TableActorContacts, schema.FieldContactActor, actor)
	if err != nil {
		return nil, err
	}
	out := []ActorContact{}
	for _, c := range rows {
		out = append(out, ActorContact{ID: c.ID.String(), Type: work.Field(c, schema.FieldContactType), Value: work.Field(c, schema.FieldContactValue),
			Label: work.Field(c, schema.FieldContactLabel), Primary: work.Field(c, schema.FieldContactPrimary) == "true",
			Identity: work.Field(c, schema.FieldContactIdentity) == "true"})
	}
	slices.SortFunc(out, func(x, y ActorContact) int {
		if c := strings.Compare(x.Type, y.Type); c != 0 {
			return c
		}
		if x.Primary != y.Primary { // the primary first within its type (TP-3)
			if x.Primary {
				return -1
			}
			return 1
		}
		return strings.Compare(x.Value, y.Value)
	})
	return out, nil
}

// periodsOf is actor's stints, with the read-time one of TP-22.
func (o ops) periodsOf(ctx context.Context, actor row.Row) ([]ActorPeriod, error) {
	rows, err := o.profileRows(ctx, schema.TableActorPeriods, schema.FieldPeriodActor, actor.ID)
	if err != nil {
		return nil, err
	}
	ps, err := teamprofile.PeriodsOf(actor, rows)
	if err != nil {
		return nil, err
	}
	out := []ActorPeriod{}
	for _, p := range ps {
		ap := ActorPeriod{Type: p.Type, StartedOn: p.StartedOn.Format(value.DateLayout), OrgRole: p.OrgRole, EndReason: p.EndReason, Source: p.Source}
		if !p.Open() {
			ap.EndedOn = p.EndedOn.Format(value.DateLayout)
		}
		if !p.Synthesised() {
			ap.ID = p.ID.String()
		}
		out = append(out, ap)
	}
	return out, nil
}

// membershipsOf is actor's project memberships: open first, then ended,
// each by start.
func (o ops) membershipsOf(ctx context.Context, actor ids.AnyID) ([]ActorMemberOut, error) {
	rows, err := o.profileRows(ctx, schema.TableProjectMembers, schema.FieldMemberActor, actor)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	if len(rows) > 0 {
		projects, perr := work.ListAll(ctx, o.env, schema.TableProjects, "")
		if perr != nil {
			return nil, perr
		}
		for _, p := range projects {
			names[p.Row.ID.String()] = work.Field(p.Row, fieldName)
		}
	}
	out := []ActorMemberOut{}
	for _, m := range rows {
		pid := work.Field(m, schema.FieldMemberProject)
		name := names[pid]
		if name == "" {
			// The project row is in a store not attached here: ask the
			// host, which reads every store it can (TEAM_PROFILES 2.6),
			// and show the id only when that misses too.
			name = pid
			if pn, ok := upgrade.As[profilestore.ProjectNamer](o.env); ok {
				if got, found := pn.ProjectName(ctx, pid); found && got != "" {
					name = got
				}
			}
		}
		out = append(out, ActorMemberOut{ID: m.ID.String(), ProjectID: pid, Project: name, Roles: work.List(m, schema.FieldMemberRoles),
			StartedOn: work.Field(m, schema.FieldMemberStartedOn), EndedOn: work.Field(m, schema.FieldMemberEndedOn), EndReason: work.Field(m, schema.FieldMemberEndReason)})
	}
	slices.SortFunc(out, func(x, y ActorMemberOut) int {
		if (x.EndedOn == "") != (y.EndedOn == "") {
			if x.EndedOn == "" {
				return -1
			}
			return 1
		}
		if c := strings.Compare(x.StartedOn, y.StartedOn); c != 0 {
			return c
		}
		return strings.Compare(x.Project, y.Project)
	})
	return out, nil
}

// fillKey sets a's stable key and handle from the first copy of the same
// actor (store precedence order) that has them, when a's own copy has
// none (FB-ACTOR-KEY).
func fillKey(a *Actor, rows []work.Sourced) {
	for _, s := range rows {
		if s.Row.ID.String() != a.ID {
			continue
		}
		if a.StableKey == "" {
			a.StableKey = work.Field(s.Row, fieldStableKey)
		}
		if a.Handle == "" {
			a.Handle = work.Field(s.Row, fieldHandle)
		}
	}
}
