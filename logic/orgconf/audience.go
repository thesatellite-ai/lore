package orgconf

// audience.go — who in an org uses a store of its lore-org.yaml
// (devco D-442). An org keeps servers, domains and accounts in a store
// only its admins can read; before `audience:`, that store sat in the
// shared file like any other, so every member's lore cloned it, failed,
// and treated their own repos as client repos (the link index could not
// be read: fail closed). A store marked `audience: admins` is copied into
// an admin's org entry only; a member's entry never lists it, so nothing
// on a member's machine tries to read it.
//
//	lore-org.yaml stores ─▶ Shared.ForPerson(viewer) ─▶ the entry's stores
//	                                                └▶ skipped (doctor's note)
//
// devco D-727: `audience: team:@<handle>` keeps a store to one roster
// team (and the admins). A member outside the team never has it in their
// org entry, so their lore never clones, fetches or names it (not even in
// doctor's note: they have no reason to know it exists); a member who
// joins the team gets it at their next open after the roster change.

import (
	"fmt"
	"slices"
	"strings"
)

// Audience is a store's `audience:` in lore-org.yaml: who in the org uses
// it. The empty value (the key left out) is every member.
type Audience string

// Audience values. A value this lore does not know (a kind a newer lore
// added, or a typo) is kept as it is, in an explicit "unknown" state
// (Unknown): a member never uses the store (fail safe, as for admins),
// and whoami and doctor say which store it is and that this lore must be
// upgraded to read its audience (devco D-734: lore 0.1.18 met team:@web,
// read it as admins and left a team member without the store, saying
// nothing).
const (
	// AudienceEveryone is the default: every member of the org uses the
	// store. It is written as no key at all.
	AudienceEveryone Audience = ""
	// AudienceAdmins: only people whose open roster stint has org_role
	// admin use the store.
	AudienceAdmins Audience = "admins"
)

// audienceTeamPrefix starts a team audience: "team:@" and the roster
// team's handle (D-727). Only that team's members and the org's admins
// use the store.
const audienceTeamPrefix = "team:@"

// TeamAudience is the audience of the roster team handle (with or
// without its @).
func TeamAudience(handle string) Audience {
	return Audience(audienceTeamPrefix + strings.TrimPrefix(strings.TrimSpace(handle), "@"))
}

// Team is the team handle (no @) of a team audience; ok is false for
// every other audience.
func (a Audience) Team() (handle string, ok bool) {
	h, ok := strings.CutPrefix(string(a), audienceTeamPrefix)
	if !ok || !validTeamHandle(h) {
		return "", false
	}
	return h, true
}

// teamHandleChars are the characters of a roster team handle (ops/team
// handleChars: one lowercase word).
const teamHandleChars = "abcdefghijklmnopqrstuvwxyz0123456789_-"

// validTeamHandle is the roster's handle rule (ops/team teamHandle): one
// lowercase word of letters, digits, '_' and '-', starting with a letter
// or digit.
func validTeamHandle(h string) bool {
	if h == "" || strings.Trim(h, teamHandleChars) != "" {
		return false
	}
	c := h[0]
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}

// Known reports whether a is an audience lore knows: everyone, admins or
// a team audience with a valid handle.
func (a Audience) Known() bool {
	if a == AudienceEveryone || a == AudienceAdmins {
		return true
	}
	_, ok := a.Team()
	return ok
}

// Unknown reports whether a is an audience this lore does not know
// (D-734): the store is kept from every member who is not an admin, and
// whoami and doctor name it with the upgrade to run.
func (a Audience) Unknown() bool { return !a.Known() }

// WellFormed reports whether a can be an audience value at all: one word
// with no space or control character (any kind, known or not). A config
// entry with such a value validates, so an admin's lore copies a store of
// an audience kind it does not know instead of failing the whole copy
// (D-734).
func (a Audience) WellFormed() bool {
	if len(a) > maxAudienceLen {
		return false
	}
	for _, r := range string(a) {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

// maxAudienceLen bounds an audience value (a team handle is far shorter).
const maxAudienceLen = 128

// Viewer is what decides which of an org's stores one person uses: an
// admin uses every store; a member uses the everyone stores and those of
// the teams (handles, no @) they belong to.
type Viewer struct {
	Admin bool
	Teams []string
}

// For reports whether the person v uses a store of audience a. An
// audience lore does not know is the admins' (fail safe).
func (a Audience) For(v Viewer) bool {
	if v.Admin || a.ForMembers() {
		return true
	}
	if h, ok := a.Team(); ok {
		return slices.Contains(v.Teams, h)
	}
	return false
}

// audienceEveryoneWord is how a command names AudienceEveryone
// (`--audience everyone`); the file leaves the key out.
const audienceEveryoneWord = "everyone"

// AudienceWords are the values a command takes, for hints.
var AudienceWords = []string{audienceEveryoneWord, string(AudienceAdmins), audienceTeamPrefix + "<team>"}

// ParseAudience reads a command's --audience value ("everyone", "admins"
// or "team:@<handle>", any case; the @ may be left out).
func ParseAudience(s string) (Audience, error) {
	w := strings.ToLower(strings.TrimSpace(s))
	switch w {
	case audienceEveryoneWord, "":
		return AudienceEveryone, nil
	case string(AudienceAdmins):
		return AudienceAdmins, nil
	}
	if h, ok := strings.CutPrefix(w, "team:"); ok {
		a := TeamAudience(h)
		if _, valid := a.Team(); !valid {
			return "", fmt.Errorf("audience %q: the team handle must be one lowercase word (letters, digits, '_' and '-'), e.g. team:@web", s)
		}
		return a, nil
	}
	return "", fmt.Errorf("audience %q: use one of %s", s, strings.Join(AudienceWords, ", "))
}

// ForMembers reports whether a member who is not an admin uses the store.
func (a Audience) ForMembers() bool { return a == AudienceEveryone }

// String is the word a person reads: "everyone" or "admins".
func (a Audience) String() string {
	if a == AudienceEveryone {
		return audienceEveryoneWord
	}
	return string(a)
}

// AdminsOnly reports whether s is used only by the org's admins. The org
// folder's store (the roster and company rules every member needs) never
// is, whatever its audience says. A team store is not (D-727): its team's
// members read it and link repos to it.
func (s Store) AdminsOnly() bool {
	_, team := s.Audience.Team()
	return s.Restricted() && !team
}

// Restricted reports whether some members of the org do not use s: an
// admins-only or a team store. The org folder's store never is.
func (s Store) Restricted() bool { return s.OrgFolder == "" && !s.Audience.ForMembers() }

// HasRestricted reports whether sh lists a store some members do not use
// (so the caller has to know the person's role and teams).
func (sh Shared) HasRestricted() bool {
	for _, s := range sh.Stores {
		if s.Restricted() {
			return true
		}
	}
	return false
}

// ForPerson is sh as one person's org entry holds it: an admin's copy is
// the whole file; a member's leaves out every store they do not use. The
// admins-only ones and those of an audience this lore does not know
// (Unknown, D-734) come back as skipped (in file order) for whoami's and
// doctor's notes; a team store of a team they are not in is left out
// without a word (D-727: they never hear of it).
func (sh Shared) ForPerson(v Viewer) (Shared, []Store) {
	if v.Admin || !sh.HasRestricted() {
		return sh, nil
	}
	out := sh
	out.Stores = nil
	var skipped []Store
	for _, s := range sh.Stores {
		if s.Restricted() && !s.Audience.For(v) {
			if s.AdminsOnly() {
				skipped = append(skipped, s)
			}
			continue
		}
		out.Stores = append(out.Stores, s)
	}
	return out, skipped
}
