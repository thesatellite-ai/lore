//go:build journeys && !windows

package journeys

// twoorgs_apart_test.go — two orgs on one laptop must never mix (devco
// N-3, N-4, N-6, N-12). Lee is the admin of two orgs (acmeteam for
// ~/code/app, betaorg for ~/code/beta); Ann is a member of acmeteam and
// joins betaorg with another address.
//
//	N-3   a sync in an acmeteam repo pushed betaorg's waiting store commit
//	      and named betaorg's store in its summary
//	N-4   Ann's join lines for betaorg carried her acmeteam git email; the
//	      join then failed and only asked the admin to add that address
//	N-6   the betaorg store clone kept the global (acmeteam) git email
//	      after a clean join
//	D-278 a sync in an acmeteam repo still fetched (read only) betaorg's
//	      store clone while resolving the org
//	N-12  identity set --org --key (an email change) dropped the name; the
//	      store clone's git user.name became the org's name

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Names of the two-orgs journey (placeholders).
const (
	toBetaAppSlug = "github.com/beta-co/app"
	toAppFolder   = "~/code/app/**"
	toBetaFolder  = "~/code/beta/**"
	toAnnBeta     = "ann@beta.example.test"
	toAnnNew      = "ann.able@example.test"
)

func TestJourneyTwoOrgsKeptApart(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	leeApp := tt.repo[tlLee.Email].Dir
	beta := w.Remote(jSecondSlug)
	betaApp := w.Remote(toBetaAppSlug)
	betaDir := func(m *Machine) string {
		c := m.Clone(betaApp, "beta")
		d := filepath.Join(m.Home, "code", "beta")
		m.MoveClone(t, c, d)
		return d
	}
	leeBeta := betaDir(lee)
	var ann *Machine
	var annApp, annBeta string

	w.Step("day1/lee-runs-two-orgs", func() {
		tlStartTeam(t, lee, leeApp, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{toAppFolder})
		tlStartTeam(t, lee, leeBeta, jSecondSetup, jSecondOrg, beta.SSHURL(), tlPerson{Name: tlLee.Name, Email: jSecondEmail, Login: tlLee.Login}, []string{toBetaFolder})
	})

	w.Step("day1/n3-sync-sends-only-this-orgs-stores", func() {
		lee.Lore(leeBeta, "team", "add", "--handle", "core", "--name", "Core", "--yes").OK(t)
		before := beta.Rev("refs/heads/main")
		lee.Lore(leeApp, "sync").OK(t).Never(t, "lore-beta", jSecondOrg)
		// --trace names the store calls too (the finding's trace showed
		// only the code repo's git calls), and never a push of betaorg's
		tr := lee.Lore(leeApp, "sync", "--trace").OK(t).Says(t, "trace: git ", " fetch ")
		for _, l := range strings.Split(tr.Stderr, "\n") {
			if strings.Contains(l, " push ") && strings.Contains(l, "lore-beta") {
				t.Fatalf("N-3: a sync in %s's repo pushed %s's store: %s", tlOrg, jSecondOrg, l)
			}
		}
		if got := beta.Rev("refs/heads/main"); got != before {
			t.Fatalf("N-3: a sync in %s's repo pushed %s's store (main %s -> %s)", tlOrg, jSecondOrg, before, got)
		}
		// D-278: a command in acmeteam's repo makes no git call at all to
		// betaorg's store (fetch, ls-remote, a read of its clone): resolving
		// the org never needs another org's clone
		for _, args := range [][]string{{"sync"}, {"sync", "--trace"}, {"whoami"}} {
			before := len(w.GitCalls(lee.Name))
			lee.Lore(leeApp, args...).OK(t)
			if got := toCallsTouching(w.GitCalls(lee.Name)[before:], "lore-beta", leeBeta); len(got) > 0 {
				t.Fatalf("D-278: lore %s in %s's repo made %d git call(s) to %s's store or repo, want 0:\n%s", strings.Join(args, " "), tlOrg, len(got), jSecondOrg, strings.Join(got, "\n"))
			}
		}
		lee.Lore(leeBeta, "sync", "--trace").OK(t).Says(t, "trace: git ", " push ")
		if beta.Rev("refs/heads/main") == before {
			t.Fatalf("a sync in %s's repo did not send its own waiting store commit", jSecondOrg)
		}
	})

	w.Step("day2/ann-joins-acmeteam", func() {
		tt.lore(tlLee, "member", "add", "--email", tlAnn.Email, "--name", tlAnn.Name, "--handle", "ann", "--role", "member", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		ann = tt.person(tlAnn)
		annApp = tt.repo[tlAnn.Email].Dir
		tlJoin(t, ann, annApp, tlTeamSetup, tt.store.SSHURL(), tlAnn, []string{toAppFolder})
	})

	w.Step("day2/n4-n6-ann-joins-betaorg-as-her-beta-self", func() {
		lee.Lore(leeBeta, "member", "add", "--email", toAnnBeta, "--name", tlAnn.Name, "--handle", "ann", "--role", "member", "--yes").OK(t)
		lee.Lore(leeBeta, "sync").OK(t)
		annBeta = betaDir(ann)
		// N-4: the identity line never carries her acmeteam address
		ann.Lore(annBeta, "setup", "join", "--invite", beta.SSHURL()).OK(t).
			Says(t, "<your email in ", "is your identity in org "+tlOrg).
			Never(t, `"email":"`+tlAnn.Email+`"`)
		// with her acmeteam address the join names the identity step first;
		// she answers it again with her betaorg address, as the hint says
		tlFirstSteps(t, ann, annBeta, jSecondSetup, "member", tlAnn)
		ann.Lore(annBeta, "setup", "apply", jSecondSetup, "--step", "join", "--answers", `{"invite":"`+beta.SSHURL()+`"}`, "--yes").
			Refused(t, "is not a member of "+jSecondOrg, "--setup "+jSecondSetup+" --step identity --answers", "lore member add --email "+tlAnn.Email)
		tlApply(t, ann, annBeta, jSecondSetup, "identity", map[string]any{"name": tlAnn.Name, "email": toAnnBeta})
		tlApply(t, ann, annBeta, jSecondSetup, "join", map[string]any{"invite": beta.SSHURL()})
		// N-6: the betaorg clone commits as her betaorg self from the join
		// step on, before any later write or sync there
		if got := toCloneConfig(t, ann, beta, "user.email"); got != toAnnBeta {
			t.Fatalf("N-6: %s's store clone has git user.email %q right after the join step, want %q", jSecondOrg, got, toAnnBeta)
		}
		tlLastSteps(t, ann, annBeta, jSecondSetup, []string{toBetaFolder})
		ann.Lore(annBeta, "doctor", "--all").Never(t, "store_identity")
	})

	w.Step("day3/n12-email-change-keeps-the-name", func() {
		tt.lore(tlLee, "actor", "contact", "add", "--actor", "@ann", "--type", "email", "--value", toAnnNew, "--primary", "--identity", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		ann.Lore(annApp, "identity", "set", "--org", tlOrg, "--key", "human:"+toAnnNew, "--yes").OK(t).Says(t, tlAnn.Name, toAnnNew)
		ann.Lore(annApp, "sync").OK(t).Never(t, "git user set to "+tlOrg)
		ann.Lore(annApp, "whoami").OK(t).Says(t, tlAnn.Name)
		if got := toCloneConfig(t, ann, tt.store, "user.name"); got == tlOrg {
			t.Fatalf("N-12: %s's store clone has git user.name %q (the org's name), want the person's", tlOrg, got)
		}
	})
}

// toCallsTouching returns the logged git command lines that name any of
// needles (a store's path part, a repo folder): the calls one command made
// to another org's store or repo (D-278).
func toCallsTouching(calls []string, needles ...string) []string {
	var out []string
	for _, c := range calls {
		for _, n := range needles {
			if strings.Contains(c, n) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// toCloneConfig is `git config --get key` in m's clone of the store r
// (the clone under ~/.lore/stores whose origin is r), failing the test
// when m has no such clone.
func toCloneConfig(t *testing.T, m *Machine, r *teamharness.Remote, key string) string {
	t.Helper()
	var found string
	root := filepath.Join(m.Home, ".lore", "stores")
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" || !d.IsDir() || d.Name() != ".git" {
			return nil
		}
		dir := filepath.Dir(p)
		if res := m.w.TryGit(dir, "config", "--local", "--get", "remote.origin.url"); strings.TrimSpace(res.Stdout) == r.SSHURL() {
			found = dir
		}
		return filepath.SkipDir
	})
	if found == "" {
		t.Fatalf("no store clone of %s under %s", r.SSHURL(), root)
	}
	return strings.TrimSpace(m.w.TryGit(found, "config", "--local", "--get", key).Stdout)
}
