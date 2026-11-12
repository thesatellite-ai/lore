package wire

// storeidentity.go — the git identity of a store clone (owner run
// 2026-11-12, D-af). A store clone is a plain checkout a person commits in
// by hand; without a repo-local identity git used their global one (a
// personal email in an org store). When lore clones a store, or (on a write
// to it, `lore sync` or another explicit setup; never on a read, owner
// bug 6) finds a clone that has no repo-local user.email (or still the one lore wrote,
// and the identity changed since: the org entry is written after the team
// store is cloned), it writes user.name and user.email into that clone's
// .git/config (never the global git config) and says so in one line
// (adapters/backend/git/cloneidentity.go):
//
//	storeOpeners ─▶ git.WithCloneIdentity(storeSigner.cloneIdentity)
//	   org store   ─▶ the org identity (orgs.<org>.identity: your name and
//	                  email in that org), the one lore signs its commits with
//	   other store ─▶ your lore identity (`lore whoami`, the setup's identity)
//	newOpeners ─▶ the clone got one ─▶ notice: clone, name <email>, why
//
// lore doctor's store_identity check compares each clone's effective
// identity (`git config user.email` there) with that same identity and
// --fix writes it (doctor_storeidentity.go).

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/store"
)

// cloneIdentity is the identity a store clone at remote gets: the org
// identity for an org store, else the lore identity; zero when neither
// resolves (the clone then keeps git's own rules).
func (s storeSigner) cloneIdentity(ctx context.Context, remote string) git.CloneIdentity {
	if sig, ok := s.owner(ctx, remote); ok {
		why := "your identity in this store's org, the one lore signs its commits with"
		if org := orgOfURL(s.opts.Home, remote); org != "" {
			why = "your identity in org " + org + " (orgs." + org + ".identity in ~/.lore/config.yaml), the one lore signs this store's commits with"
		}
		return git.CloneIdentity{Signature: sig, Why: why}
	}
	sig := s.fallback(ctx)
	if agentSignature(sig.Name, sig.Email) {
		return git.CloneIdentity{} // no person address (a CI or bare agent key): git's own rules stay
	}
	return git.CloneIdentity{Signature: sig, Why: "your lore identity (`lore whoami`), the one lore signs this store's commits with"}
}

// ownedByOrg reports whether an org identity signs the store at remote.
func (s storeSigner) ownedByOrg(ctx context.Context, remote string) bool {
	_, ok := s.owner(ctx, remote)
	return ok
}

// agentSignature reports a git identity that names an agent rather than
// a person: an address at lore's non-routable domain
// (agent.auto/<tool>/…@lore.invalid) or a "<person> via <tool>" name.
// lore never writes one into a store clone and doctor flags it (owner bug
// 12).
func agentSignature(name, email string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(email)), "@"+identity.CommitEmailDomain) || strings.Contains(name, viaTool)
}

// cloneIdentityLine is the one line said when a store clone got its git
// identity.
// It names the running command (wire/cmdname.go).
func cloneIdentityLine(ctx context.Context, dir string, id git.CloneIdentity) string {
	was := ""
	if id.Was != "" {
		was = " (it was " + id.Was + ", which lore had set before)"
	}
	return CommandPrefix(ctx) + "set user.name and user.email in the store clone " + dir + " (.git/config) to " + id.Name + " <" + id.Email + ">" + was + ": " + id.Why +
		", so your own git commits there carry it (your global git config is unchanged)"
}

// sayCloneIdentity says cloneIdentityLine when opening b wrote the clone's
// identity; nil notice says nothing.
func sayCloneIdentity(ctx context.Context, b store.Backend, notice func(string)) {
	gb := gitOf(b)
	if gb == nil || notice == nil {
		return
	}
	if id, ok := gb.IdentitySetUp(); ok {
		notice(cloneIdentityLine(ctx, gb.Dir(), id))
	}
}

// cloneIdentitySay is the option that says cloneIdentityLine when a write
// or `lore sync` gave an existing clone its identity (a read's open leaves
// it to them, owner bug 6); nil notice says nothing.
func cloneIdentitySay(notice func(string)) git.Option {
	return git.WithCloneIdentitySay(func(ctx context.Context, dir string, id git.CloneIdentity) {
		if notice != nil {
			notice(cloneIdentityLine(ctx, dir, id))
		}
	})
}
