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
//	   personal store ─▶ the identity of the setup that owns it (item 39,
//	                  storeowner.go), never the setup of the folder lore runs in
//	   other store ─▶ your lore identity (`lore whoami`, the setup's identity)
//	newOpeners ─▶ the clone got one ─▶ notice: clone, name <email>, why
//
// lore doctor's store_identity check compares each clone's effective
// identity (`git config user.email` there) with that same identity and
// --fix writes it (doctor_storeidentity.go).

import (
	"context"
	"os"
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
		why, who := "your identity in this store's org, the one lore signs its commits with", "org identity"
		if org := orgOfURL(s.opts.Home, remote); org != "" {
			why = "your identity in org " + org + " (orgs." + org + ".identity in ~/.lore/config.yaml), the one lore signs this store's commits with"
			who = "your identity in org " + org
		}
		return git.CloneIdentity{Signature: sig, Why: why, Who: who}
	}
	if p, ok := s.personal(ctx, remote); ok {
		return git.CloneIdentity{Signature: p.Sig, Why: p.Why + ", the one lore signs this store's commits with", Who: "personal store owner"}
	}
	sig := s.fallback(ctx)
	if agentSignature(sig.Name, sig.Email) {
		return git.CloneIdentity{} // no person address (a CI or bare agent key): git's own rules stay
	}
	return git.CloneIdentity{Signature: sig, Why: "your lore identity (`lore whoami`), the one lore signs this store's commits with", Who: "your lore identity"}
}

// personal is the identity of the setup that owns the personal store at
// remote (storeowner.go), whatever setup the folder lore runs in uses
// (owner item 39).
func (s storeSigner) personal(ctx context.Context, remote string) (personalIdentity, bool) {
	return personalStoreIdentity(ctx, s.opts.Home, optsProjectDir(s.opts), remote)
}

// identityFor is the person's signature for the store at remote: the
// person's identity in its org for an org store, the owning setup's
// identity for a personal store, else the lore identity (fallback). It
// is git's fallback author of lore's commits there and the author of a
// hand edit (adapters/backend/git identity.go personSignature).
func (s storeSigner) identityFor(ctx context.Context, remote string) git.Signature {
	if sig, ok := s.owner(ctx, remote); ok {
		return sig
	}
	if p, ok := s.personal(ctx, remote); ok {
		return p.Sig
	}
	return s.fallback(ctx)
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

// cloneIdentityLine is the one short line said when a store clone got
// its git identity (once: when lore makes the clone, or when the identity
// lore set changed). It names the running command (wire/cmdname.go), the
// clone under ~ and who the identity is; `lore doctor` (store_identity)
// and `lore where` give the long why (B4-17: the line was ~400 chars).
func cloneIdentityLine(ctx context.Context, dir string, id git.CloneIdentity) string {
	who := id.Who
	if who == "" {
		who = "your lore identity"
	}
	if id.Was != "" {
		who += ", was " + id.Was
	}
	return CommandPrefix(ctx) + "store clone " + underHome(dir) + ": git user set to " + id.Name + " <" + id.Email + "> (" + who + ")"
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
		for _, l := range strayCloneLines(gb.Dir()) { // A-16 (strayclone.go)
			notice(CommandPrefix(ctx) + l)
		}
	}
}

// cloneIdentitySay is the option that says cloneIdentityLine when a write
// or `lore sync` gave an existing clone its identity (a read's open leaves
// it to them, owner bug 6); nil notice says nothing.
func cloneIdentitySay(notice func(string)) git.Option {
	return git.WithCloneIdentitySay(func(ctx context.Context, dir string, id git.CloneIdentity) {
		if notice != nil {
			notice(cloneIdentityLine(ctx, dir, id))
			for _, l := range strayCloneLines(dir) { // A-16 (strayclone.go)
				notice(CommandPrefix(ctx) + l)
			}
		}
	})
}

// underHome shows p under the user's home as ~/… (os.UserHomeDir; an
// unknown home leaves p as it is): the one-line notices of the store
// clones stay short (B4-17, W-4).
func underHome(p string) string {
	home, _ := os.UserHomeDir() // "" leaves the path as it is
	return tildeHome(p, home)
}
