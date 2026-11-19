package profilestore

// writeguard.go — the owner's role rule as the one check of every row
// write (engine.WriteGuard; devco D-694, D-697, owner use pass 5). In an
// org store:
//
//	change of …                                   member   admin
//	a policy of company ground (every key,
//	  branch share/isolate and their undo)         refused  ok
//	a company must rule / critical-high hotfix
//	  (any field, delete, move, archive, pin)      refused  ok
//	a new rule/hotfix/memory/decision, or one
//	  raised to must, in a company folder          only in a folder of a team they are on
//	a team (create, edit, rename, remove, who
//	  is on it)                                    refused  ok
//	any other row, the repo, the personal store    ok       ok
//
// Teams are an admin's change because they are the member's add right:
// a member who could put themselves on @web could then add rules in
// acme/web, and the rule above would be only as strong as the team
// commands (devco D-694 rule gap). The one member change of a team the
// guard lets through is a person taking up their own invite: they add
// only themselves to a team the invite names (WithInviteJoin), as the
// store's authority lets it through at sync.
//
// The command's own check may name the refusal first (what only an admin
// does, the line the admin runs: WithAsk); the guard answers every write
// no command check saw, with the same wording.

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// askKey carries the refusal wording of the running command (WithAsk).
type askKey struct{}

// ask is what only an admin does and the exact line an admin runs.
type ask struct{ does, line string }

// WithAsk is ctx of a command that knows its refusal wording: does is what
// only an admin does ("change a policy of a company folder …"), line the
// exact command an admin runs, with the full --to <org>/<folder> (devco
// D-697). The write guard prints them when it refuses.
func WithAsk(ctx context.Context, does, line string) context.Context {
	return context.WithValue(ctx, askKey{}, ask{does: does, line: line})
}

func askOf(ctx context.Context) (ask, bool) {
	a, ok := ctx.Value(askKey{}).(ask)
	return a, ok
}

// Change kinds the guard tells apart (Classify).
const (
	// ChangeFree is a change every member makes.
	ChangeFree = "free"
	// ChangePolicy writes a policy of the place.
	ChangePolicy = "policy"
	// ChangePinned changes a company pinned row (any field, delete, move).
	ChangePinned = "pinned"
	// ChangeAdd adds a row (or raises one to pinned): a member does it in
	// a folder of a team they are on.
	ChangeAdd = "add"
	// ChangeTeam writes a team row (any field, a new team, a removal).
	ChangeTeam = "team"
)

// joinKey carries the invite a person is taking up (WithInviteJoin).
type joinKey struct{}

// inviteJoin is the joining actor and the team handles their invite names.
type inviteJoin struct {
	actor string
	teams []string
}

// WithInviteJoin is ctx of an invite being taken up: actor (the joiner's
// act_ id) may add themselves, and nobody else, to the teams the invite
// names (handles without "@"). Only the join writer sets it; an admin
// made the invite, so the join is the admin's change carried out.
func WithInviteJoin(ctx context.Context, actor string, teams []string) context.Context {
	return context.WithValue(ctx, joinKey{}, inviteJoin{actor: actor, teams: teams})
}

// joinsOwnInvite reports whether w only adds the joiner of ctx's invite
// to a team that invite names, every other field unchanged.
func joinsOwnInvite(ctx context.Context, w engine.GuardedWrite) bool {
	j, ok := ctx.Value(joinKey{}).(inviteJoin)
	if !ok || j.actor == "" || w.Before == nil || w.After.Deleted {
		return false
	}
	if !slices.Contains(j.teams, strings.TrimPrefix(fieldText(*w.Before, "handle"), "@")) {
		return false
	}
	for _, f := range []map[string]value.Value{w.After.Fields, w.Before.Fields} {
		for k := range f {
			if k != schema.FieldTeamMembers && fieldText(w.After, k) != fieldText(*w.Before, k) {
				return false
			}
		}
	}
	before := memberIDs(*w.Before)
	after := memberIDs(w.After)
	for _, m := range before {
		if !slices.Contains(after, m) {
			return false
		}
	}
	for _, m := range after {
		if m != j.actor && !slices.Contains(before, m) {
			return false
		}
	}
	return true
}

// memberIDs are the act_ ids of a team row's members.
func memberIDs(t row.Row) []string {
	v, ok := t.Fields[schema.FieldTeamMembers]
	if !ok || v == nil {
		return nil
	}
	return value.ItemStrings(v)
}

// Classify is the kind of one change, before the place is judged.
func Classify(w engine.GuardedWrite) string {
	switch {
	case w.After.Table == schema.TablePolicies:
		return ChangePolicy
	case w.After.Table == schema.TableTeams:
		return ChangeTeam
	case w.Before != nil && CompanyPinned(*w.Before):
		return ChangePinned
	case w.After.Deleted:
		return ChangeFree
	case w.Before == nil:
		return ChangeAdd
	case CompanyPinned(w.After):
		return ChangeAdd // raised to must / critical-high
	}
	return ChangeFree
}

// WriteGuard is the engine.WriteGuard of an Env (wire installs it with
// the App as Env).
type WriteGuard struct{ Env PlaceEnv }

// GuardWrite refuses a member's change the role rule keeps for admins
// (see the table above); nothing is written when it does.
func (g WriteGuard) GuardWrite(ctx context.Context, target store.Source, writes []engine.GuardedWrite) error {
	var kinds []string
	var hit engine.GuardedWrite
	for _, w := range writes {
		k := Classify(w)
		if k == ChangeFree || (k == ChangeTeam && joinsOwnInvite(ctx, w)) {
			continue
		}
		if len(kinds) == 0 {
			hit = w
		}
		kinds = append(kinds, k)
	}
	if len(kinds) == 0 {
		return nil
	}
	src, lv := placeOf(ctx, g.Env, target)
	if !mayBeGround(src, lv) {
		return nil
	}
	r, done, ok := openRoster(ctx, g.Env, src)
	defer done()
	if !ok || !CompanyGround(lv, src, orgOf(r.Source())) {
		return nil
	}
	a, err := ReadAdmins(ctx, r)
	if err != nil || a.May() {
		return err
	}
	// place is the real <org>/<folder>: a linked repo attaches its folder
	// under the bare org name, which names no folder (devco D-694).
	place := store.Source(labelOf(g.Env, src))
	onlyAdds := !slices.ContainsFunc(kinds, func(k string) bool { return k != ChangeAdd })
	if onlyAdds {
		teams, terr := callerTeams(ctx, r)
		if terr != nil {
			return terr
		}
		if slices.Contains(teams, folderOf(place)) {
			return nil
		}
	}
	return refusal(ctx, r, a, place, kinds[0], hit)
}

// refusal is the E_REFUSED of the guard: the command's wording (WithAsk)
// when it gave one, else what the write does.
func refusal(ctx context.Context, r Roster, a Admins, src store.Source, kind string, w engine.GuardedWrite) error {
	does, line := guardDoes(src, kind, w), "the same command"
	if k, ok := askOf(ctx); ok {
		does, line = k.does, k.line
	}
	if kind == ChangeAdd {
		does += " (a member adds rules and notes only in the folder of a team they are on, or in their personal store)"
	}
	return refuseMember(r, a, does, line)
}

// guardDoes says what a write did when no command named it.
func guardDoes(src store.Source, kind string, w engine.GuardedWrite) string {
	id := w.After.ID.String()
	switch kind {
	case ChangePolicy:
		return policyDoes + " " + string(src)
	case ChangePinned:
		return "change " + id + " (a must rule or a critical/high hotfix of the company folder " + string(src) + ", which every repo reading it is told)"
	case ChangeTeam:
		return TeamDoes(fieldText(w.After, "handle"))
	}
	return "add " + id + " to the company folder " + string(src)
}

// TeamDoes is what only an admin does to a team (the guard's wording and
// the team commands' WithAsk): who is on a team decides where they add
// rules and notes.
func TeamDoes(handle string) string {
	h := "@" + strings.TrimPrefix(handle, "@")
	return "change the team " + h + " (who is on a team decides in which company folder they add rules and notes)"
}

// policyDoes is the guard's policy wording (ops/settings policyAdminDoes).
const policyDoes = "change a policy of the company folder"

// callerTeams are the handles of the active teams the caller is on: a
// team's folder is the company folder named like its handle (@web ->
// acme/web).
func callerTeams(ctx context.Context, r Roster) ([]string, error) {
	teams, err := r.List(ctx, schema.TableTeams)
	if err != nil {
		return nil, err
	}
	me := r.Caller().String()
	var out []string
	for _, t := range teams {
		if !onTeam(t, me) {
			continue
		}
		out = append(out, strings.TrimPrefix(fieldText(t, "handle"), "@"))
	}
	return out, nil
}

// onTeam reports whether actor id is a member of an active team row t.
func onTeam(t row.Row, id string) bool {
	if fieldText(t, "left_at") != "" {
		return false
	}
	if v, ok := t.Fields["active"]; ok && v != nil && v.String() == "false" {
		return false
	}
	v, ok := t.Fields[schema.FieldTeamMembers]
	if !ok || v == nil {
		return false
	}
	return strings.Contains(v.String(), id)
}

// folderOf is the folder name of a placed store source ("web" of
// "acme/web"; labelOf gives that name for a linked folder attached as
// "acme"), "" for a store root.
func folderOf(src store.Source) string {
	_, f, _ := strings.Cut(string(src), "/")
	return f
}
