package wire

// storeowner.go — whose identity a personal store carries (owner item 39).
// A store clone's git identity, the commits lore makes there and the
// author of a hand edit follow the setup that OWNS the store, never the
// setup of the folder lore runs in: run from a work folder, `lore whoami`
// is the work identity, and the person's own personal store must still
// commit as their personal identity.
//
//	store url ─▶ an org store (orgs[].stores, storeSigner.owner)
//	               ─▶ the person's identity in that org (unchanged)
//	          ─▶ a personal store (personalStoreIdentity):
//	               setups[].personal_store: <url>      ─▶ that setup's identity
//	               the shared personal.store, inherited ─▶ the identity of the
//	                 solo setups that inherit it (a member or admin setup's
//	                 identity is its org's, and personal.store is not the org's)
//	               no such setup, url = personal.store  ─▶ the top-level identity:
//	          ─▶ any other store ─▶ the lore identity (`lore whoami`, as before)
//
// Several owning setups with different identities: the active one among
// them, else the default setup among them, else the first in config order.

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/onboard"
)

// personalIdentity is the identity of the setup that owns a personal
// store.
type personalIdentity struct {
	// Sig is the commit signature (the identity's name and key email).
	Sig git.Signature
	// Emails are the identity's addresses, lower case: its key's, its
	// earlier keys' and its extra emails. doctor accepts any of them in
	// the clone (the person set one by hand).
	Emails []string
	// Why says whose identity it is, for the clone line and doctor.
	Why string
}

// personalStoreIdentity is the identity of the setup that owns the
// personal store at storeURL (see the file comment); ok is false when
// storeURL is nobody's personal store or the owner has no identity with
// an email.
func personalStoreIdentity(ctx context.Context, home, dir, storeURL string) (personalIdentity, bool) {
	if strings.TrimSpace(storeURL) == "" {
		return personalIdentity{}, false
	}
	cfg, err := ReadSetupsConfig(home)
	if err != nil {
		return personalIdentity{}, false
	}
	shared := ""
	if mc, merr := readMachineConfig(home); merr == nil {
		shared = mc.personalURL()
	}
	type owner struct {
		e  onboard.SetupEntry
		id onboard.SetupIdentity
	}
	var owners []owner
	for _, s := range cfg.Setups {
		if s.Disabled {
			continue
		}
		owned, inherited := ownsPersonalStore(s, storeURL, shared)
		if !owned || (inherited && s.Kind.HasOrg()) {
			continue
		}
		if id := cfg.IdentityOf(s); strings.TrimSpace(id.Key) != "" {
			owners = append(owners, owner{s, id})
		}
	}
	if len(owners) == 0 {
		top := cfg.TopIdentity
		if shared == "" || !sameStore(storeURL, shared, "") || strings.TrimSpace(top.Key) == "" {
			return personalIdentity{}, false
		}
		return personalOf(top, "your identity (identity: in ~/.lore/config.yaml), the owner of this personal store (personal.store)")
	}
	pick := owners[0]
	if slices.ContainsFunc(owners[1:], func(o owner) bool { return !sameKey(o.id.Key, pick.id.Key) }) {
		named := func(name string) int {
			return slices.IndexFunc(owners, func(o owner) bool { return name != "" && o.e.Name == name })
		}
		active := ""
		if res, rerr := ResolveSetup(ctx, setupQueryFor(ctx, home, dir, nil)); rerr == nil {
			active = res.Active.Name
		}
		if i := named(active); i >= 0 {
			pick = owners[i]
		} else if i := slices.IndexFunc(owners, func(o owner) bool { return o.e.Default }); i >= 0 {
			pick = owners[i]
		}
	}
	where := "setups." + pick.e.Name + ".identity"
	if pick.e.Kind.HasOrg() && pick.e.Identity.IsZero() {
		where = "orgs." + pick.e.Org + ".identity"
	}
	return personalOf(pick.id, "your identity in setup "+pick.e.Name+" ("+where+" in ~/.lore/config.yaml), the setup whose personal store this is")
}

// personalOf is id as a personalIdentity; ok is false when its key holds
// no email.
func personalOf(id onboard.SetupIdentity, why string) (personalIdentity, bool) {
	r := identity.Resolved{StableKey: strings.TrimSpace(id.Key), DisplayName: strings.TrimSpace(id.Name), Aliases: id.Aliases}
	name, email := r.CommitSignature()
	if !strings.Contains(email, "@") || strings.HasSuffix(strings.ToLower(email), "@"+identity.CommitEmailDomain) {
		return personalIdentity{}, false
	}
	emails := r.PersonEmails()
	for _, e := range id.Emails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" && !slices.Contains(emails, e) {
			emails = append(emails, e)
		}
	}
	return personalIdentity{Sig: git.Signature{Name: name, Email: email}, Emails: emails, Why: why}, true
}

// sameKey reports whether two identity keys name one identity (a bare
// email and human:<email> are the same; case is ignored).
func sameKey(a, b string) bool {
	norm := func(k string) string {
		k = strings.ToLower(strings.TrimSpace(k))
		return strings.TrimPrefix(k, identity.KindHuman+":")
	}
	return norm(a) == norm(b)
}
