package wire

// rostermember.go — a roster member named by @handle or email, whatever
// their status (B-12). The hand-over after `lore member remove` names the
// person who left: `lore task reassign --from @ruth` and
// `lore task list --assignee ruth@example.com` must still find them,
// because their row stays on the roster for history. `--assign` keeps
// resolving active people only (ResolveHandle): nobody is given work
// after they left.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

var _ opstask.RosterRefer = (*App)(nil)

// stableKeyKindSep splits a stable key's kind from its name
// ("human:ana@example.com").
const stableKeyKindSep = ":"

// memberRef is what a reference to a roster member names: a handle (no @)
// or an email (lower case); exactly one is set.
type memberRef struct {
	handle string
	email  string
}

// parseMemberRef reads "@ana", "ana@example.com" or "@ana@example.com" (a
// stray @ before an email); ok is false for anything else (an actor id, a
// display name, a stable key with a kind).
func parseMemberRef(raw string) (memberRef, bool) {
	v := strings.TrimSpace(raw)
	if strings.Contains(v, stableKeyKindSep) {
		return memberRef{}, false
	}
	rest, marked := strings.CutPrefix(v, rosterHandleMark)
	switch {
	case rest == "":
		return memberRef{}, false
	case strings.Contains(rest, rosterHandleMark):
		return memberRef{email: strings.ToLower(rest)}, true
	case marked:
		return memberRef{handle: rest}, true
	}
	return memberRef{}, false
}

// matches reports whether roster actor x is the member m names.
func (m memberRef) matches(x RosterActor) bool {
	if m.handle != "" {
		return x.Handle == m.handle
	}
	_, email, _ := strings.Cut(x.StableKey, stableKeyKindSep)
	return email != "" && strings.EqualFold(email, m.email)
}

// shown is the reference as output names it.
func (m memberRef) shown() string {
	if m.handle != "" {
		return rosterHandleMark + m.handle
	}
	return m.email
}

// rosterMember finds the member raw names on the roster @handles resolve
// on (coordRoster), active first, else one who left. ok is false for a
// reference that is no handle or email (the caller resolves it its own
// way) and for no match; r is the roster read, for the caller's miss text.
func (a *App) rosterMember(ctx context.Context, raw string) (x RosterActor, r rosterAt, ok bool, err error) {
	m, isRef := parseMemberRef(raw)
	if !isRef {
		return RosterActor{}, rosterAt{}, false, nil
	}
	if r, err = a.coordRoster(ctx); err != nil {
		return RosterActor{}, r, false, err
	}
	actors, err := a.RosterActors(ctx, r.src)
	if err != nil {
		return RosterActor{}, r, false, err
	}
	var gone *RosterActor
	for i := range actors {
		if !m.matches(actors[i]) {
			continue
		}
		if actors[i].Active {
			return actors[i], r, true, nil
		}
		if gone == nil {
			gone = &actors[i]
		}
	}
	if gone != nil {
		return *gone, r, true, nil
	}
	return RosterActor{}, r, false, nil
}

// RosterActorRef implements opstask.RosterRefer: the actor id of the
// roster member raw names (@handle or email), people who left included.
// A roster that cannot be read is no match: the caller's own lookup
// answers.
func (a *App) RosterActorRef(ctx context.Context, raw string) (string, bool, error) {
	x, _, ok, err := a.rosterMember(ctx, raw)
	if err != nil || !ok {
		return "", false, nil
	}
	return x.ID, true, nil
}

// memberMissErr refuses a --from naming no one on r, in the form given
// (an email is never shown as "@email").
func memberMissErr(r rosterAt, raw string) error {
	m, _ := parseMemberRef(raw)
	return errs.New(errs.NotFound, "no one is "+m.shown()+" on the roster in "+r.shown()+", now or before").
		WithHint("lore member list shows the roster; an actor id (act_…) works too")
}
