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
//	lore-org.yaml stores ─▶ Shared.ForPerson(admin) ─▶ the entry's stores
//	                                               └▶ skipped (doctor's note)

import (
	"fmt"
	"strings"
)

// Audience is a store's `audience:` in lore-org.yaml: who in the org uses
// it. The empty value (the key left out) is every member.
type Audience string

// Audience values. Closed set: a value lore does not know is read as
// AudienceAdmins (ForMembers), so a member never tries a store the file
// does not plainly open to them.
const (
	// AudienceEveryone is the default: every member of the org uses the
	// store. It is written as no key at all.
	AudienceEveryone Audience = ""
	// AudienceAdmins: only people whose open roster stint has org_role
	// admin use the store.
	AudienceAdmins Audience = "admins"
)

// audienceEveryoneWord is how a command names AudienceEveryone
// (`--audience everyone`); the file leaves the key out.
const audienceEveryoneWord = "everyone"

// AudienceWords are the values a command takes, for hints.
var AudienceWords = []string{audienceEveryoneWord, string(AudienceAdmins)}

// ParseAudience reads a command's --audience value ("everyone" or
// "admins", any case).
func ParseAudience(s string) (Audience, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case audienceEveryoneWord, "":
		return AudienceEveryone, nil
	case string(AudienceAdmins):
		return AudienceAdmins, nil
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
// is, whatever its audience says.
func (s Store) AdminsOnly() bool { return s.OrgFolder == "" && !s.Audience.ForMembers() }

// HasAdminsOnly reports whether sh lists a store only admins use (so the
// caller has to know whether the person is an admin).
func (sh Shared) HasAdminsOnly() bool {
	for _, s := range sh.Stores {
		if s.AdminsOnly() {
			return true
		}
	}
	return false
}

// ForPerson is sh as one person's org entry holds it: an admin's copy is
// the whole file; a member's leaves out every admins-only store, which
// comes back as skipped (in file order) for doctor's note.
func (sh Shared) ForPerson(admin bool) (Shared, []Store) {
	if admin || !sh.HasAdminsOnly() {
		return sh, nil
	}
	out := sh
	out.Stores = nil
	var skipped []Store
	for _, s := range sh.Stores {
		if s.AdminsOnly() {
			skipped = append(skipped, s)
			continue
		}
		out.Stores = append(out.Stores, s)
	}
	return out, skipped
}
