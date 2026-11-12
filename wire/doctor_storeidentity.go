package wire

// doctor_storeidentity.go — the store_identity check of `lore doctor`
// (D-af, storeidentity.go): a store clone whose effective git identity
// (`git config user.email` there: its own .git/config, else the global
// one) is not the identity lore signs that store's commits with (your
// identity in the store's org, else your lore identity) is a warning: a
// hand commit there would carry the wrong email. The fix is the exact
// `git -C <clone> config …` line; `lore doctor --fix` writes it into the
// clone's .git/config (never the global git config).

import (
	"context"
	"slices"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
)

// DoctorCheckStoreIdentity is the check's JSON name.
const DoctorCheckStoreIdentity DoctorCheck = "store_identity"

// DoctorStoreIdentities checks each attached store clone's git identity;
// fix writes the expected one into the clones that differ.
func (a *App) DoctorStoreIdentities(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	who := storeIdentityFunc(a.opts)
	var mine []string // the person's own emails (lore whoami's person and earlier keys)
	if r, rerr := resolveIdentity(ctx, a.opts); rerr == nil {
		mine = r.PersonEmails()
	}
	seen := map[string]bool{}
	var out []DoctorFinding
	for _, src := range a.Stores() {
		gb := gitOf(st.byName[src])
		if gb == nil || !gb.CloneMode() || seen[gb.Dir()] {
			continue
		}
		dir := gb.Dir()
		seen[dir] = true
		origin := gitbackend.OriginAt(dir)
		want := who.cloneIdentity(ctx, origin)
		if strings.TrimSpace(want.Email) == "" || strings.TrimSpace(want.Name) == "" {
			continue // no identity to compare with: git's own rules stay
		}
		got := effectiveGitConfig(ctx, dir, gitbackend.ConfigUserEmail)
		gotName := effectiveGitConfig(ctx, dir, gitbackend.ConfigUserName)
		agent := agentSignature(gotName, got)
		if !agent && cloneIdentityAccepted(got, want.Email, mine, who.ownedByOrg(ctx, origin)) {
			continue
		}
		shown := got
		if shown == "" {
			shown = "no email"
		}
		msg := "the store clone " + dir + " commits as " + shown + " (git config user.email there), not as " + want.Email + ", " + want.Why + "; a commit made by hand there would carry " + shown
		if agent {
			msg = "the store clone " + dir + " commits as the agent identity " + gotName + " <" + got + "> (git config user.name/user.email there); a git identity in a clone is the person's (" + want.Name + " <" + want.Email + ">, " + want.Why + "), the agent goes into commit trailers only"
		}
		f := DoctorFinding{Check: DoctorCheckStoreIdentity, Level: DoctorWarning, Store: src,
			Message: msg,
			Fix:     storeIdentityFix(dir, want) + ", or lore doctor --fix"}
		if fix {
			if ferr := gitbackend.SetLocalIdentity(ctx, dir, want.Signature); ferr != nil {
				f.Message += "; the fix failed: " + ferr.Error()
			} else {
				f.Fixed = true
				f.Message = "the store clone " + dir + " committed as " + shown + "; its .git/config now names " + want.Name + " <" + want.Email + "> (" + want.Why + ")"
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// cloneIdentityAccepted reports whether a clone committing as email got
// is right: the expected identity's email, or, in a store no org identity
// signs, any of the person's own emails (a person who set their identity
// by hand, owner bug 12; the name may differ, only the email counts).
func cloneIdentityAccepted(got, want string, mine []string, org bool) bool {
	got = strings.ToLower(strings.TrimSpace(got))
	if got == "" {
		return false
	}
	if strings.EqualFold(got, strings.TrimSpace(want)) {
		return true
	}
	return !org && slices.Contains(mine, got)
}

// storeIdentityFix is the exact commands that give the clone at dir the
// identity want (its own .git/config only).
func storeIdentityFix(dir string, want gitbackend.CloneIdentity) string {
	return "git -C " + shellWord(dir) + " config user.email " + shellWord(want.Email) +
		" && git -C " + shellWord(dir) + " config user.name " + shellWord(want.Name)
}

// effectiveGitConfig is git's value of key in dir as a commit there reads
// it (the clone's own config, else global and system); "" when unset.
func effectiveGitConfig(ctx context.Context, dir, key string) string {
	out, err := gitRun{dir: dir, args: []string{"config", "--get", key}}.output(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
