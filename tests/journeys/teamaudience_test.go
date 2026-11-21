//go:build journeys && !windows

package journeys

// teamaudience_test.go — a store kept to one roster team (devco:D-727).
// Lee's org keeps a client's notes in a second store only the @secret team
// (and the admins) can read on the forge. With audience everyone, every
// member's lore tried to clone it and complained; `audience: admins` would
// shut out the team that works on it. Now the store is for team:@secret:
// Ann (a member outside the team, no forge access) never clones, fetches
// or hears of it; once Lee adds her to @secret her next sync brings it in,
// and when she leaves the team her next sync drops it again (her clone
// stays on disk, no longer read).

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	taSecretSlug = "github.com/acme-co/lore-secret"
	taSecretBody = "the client portal deploys from the blue cluster"
	// taSecretPhrase is a part of taSecretBody that is not the query a
	// reader searches for ("blue cluster"): an empty answer echoes the
	// query words (devco:D-821), so a search's checks look for this phrase
	// and the row's id, either way round (devco:D-832)
	taSecretPhrase = "client portal deploys"
)

func TestJourneyTeamAudienceStore(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	sec := w.Remote(taSecretSlug)
	parent := filepath.Join(lee.Home, "code")
	line, secretID := "", ""

	w.Step("day1/lee-keeps-a-store-to-the-secret-team", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "init", "--non-interactive", "--name=app").OK(t)
		c := tt.repo[tlLee.Email]
		c.CommitAll("lore init")
		c.Git("push", "-q", "origin", "HEAD")
		// a team the roster does not hold is refused, naming the teams
		tt.lore(tlLee, "org", "store", "add", "--org", tlOrg, "--url", sec.SSHURL(), "--audience", "team:@secret", "--yes").
			Refused(t, "no team @secret", "lore team add --handle @secret")
		tt.lore(tlLee, "team", "add", "--handle", "@secret", "--members", "@lee", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		tt.lore(tlLee, "org", "store", "add", "--org", tlOrg, "--url", sec.SSHURL(), "--audience", "team:@secret", "--push", "--yes").OK(t).
			Says(t, "team:@secret", "only @secret's members and the org's admins")
		if got, _ := tt.store.Read("refs/heads/main", jManyOrgFile); !strings.Contains(got, "audience: team:@secret") {
			t.Fatalf("lore-org.yaml lacks the team audience:\n%s", got)
		}
		lee.Lore(parent, "store", "folder", "add", sec.SSHURL(), "client").OK(t)
		added := lee.Lore(parent, "memory", "add", "--body", taSecretBody, "--to", "lore-secret/client", "--yes").OK(t)
		if secretID = regexp.MustCompile(`mem_[0-9a-f]{32}`).FindString(added.Out()); secretID == "" {
			t.Fatalf("no memory id in: %s", added.Out())
		}
		lee.Lore(parent, "sync").OK(t)
		_, line = tlInvite(t, lee, c.Dir, tlAnn.Email)
	})

	ann := tt.person(tlAnn)
	asDeny(t, ann, taSecretSlug)
	ann.TraceGit(t) // every git process on Ann's machine from here on

	w.Step("day1/ann-outside-the-team-never-touches-it", func() {
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		ann.Lore(filepath.Join(ann.Home, "code"), "org", "refresh", tlOrg).OK(t).Never(t, "lore-secret", "cannot read", "ERROR")
		a := tt.repo[tlAnn.Email]
		a.Git("pull", "-q", "--no-rebase", "origin", "main")
		tt.lore(tlAnn, "sync").OK(t).Never(t, "lore-secret", "could not", "problem")
		tt.lore(tlAnn, "whoami").OK(t).Never(t, "lore-secret")
		tt.lore(tlAnn, "doctor").Never(t, "lore-secret", "store_access")
		// the empty answer echoes the query words in its read-as and try
		// lines (devco:D-821); the row itself (its id, its body past the
		// query words) never shows (devco:D-832)
		tt.lore(tlAnn, "search", "blue cluster").OK(t).Says(t, "(no matches)").Never(t, secretID, taSecretPhrase)
		if cfg := asConfig(t, ann); strings.Contains(cfg, "lore-secret") {
			t.Fatalf("Ann's org entry lists the @secret store:\n%s", cfg)
		}
		// D-727: no clone of the store anywhere under ~/.lore/stores (by
		// origin url, whatever folder it would sit in) and no git process
		// of hers ever reached for it (clone, fetch, ls-remote)
		if got := ann.ClonesOfStore(sec); len(got) > 0 {
			t.Fatalf("Ann has a clone of the @secret store at %v", got)
		}
		if got := ann.GitTouched(t, sec); len(got) > 0 {
			t.Fatalf("Ann's git reached for the @secret store:\n%s", strings.Join(got, "\n"))
		}
		// a member cannot change who uses a store
		tt.lore(tlAnn, "org", "store", "audience", "--org", tlOrg, "--url", sec.SSHURL(), "--audience", "everyone", "--yes").
			Refused(t, "only an admin of "+tlOrg)
	})

	w.Step("day1/lee-reads-it", func() {
		lee.Lore(parent, "search", "blue cluster").OK(t).Says(t, secretID, taSecretPhrase)
		lee.Lore(parent, "store", "list").OK(t).Says(t, "lore-secret", "team:@secret")
	})

	// Lee adds Ann to @secret (and the forge gives her login access): her
	// next sync brings the store in, with no command of her own.
	w.Step("day2/ann-joins-the-team-and-gets-the-store", func() {
		w.AdvanceDays(1)
		tt.lore(tlLee, "team", "member", "add", "--handle", "@secret", "--members", "@ann", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		asAllow(t, ann, taSecretSlug)
		tt.lore(tlAnn, "sync").OK(t).Never(t, "no longer read")
		if cfg := asConfig(t, ann); !strings.Contains(cfg, "lore-secret") {
			t.Fatalf("Ann's org entry lacks the @secret store after joining the team:\n%s", cfg)
		}
		tt.lore(tlAnn, "sync").OK(t)
		ann.Lore(filepath.Join(ann.Home, "code"), "search", "blue cluster").OK(t).Says(t, secretID, taSecretPhrase)
		// whoami and doctor name the team's store with who uses it
		tt.lore(tlAnn, "whoami").OK(t).Says(t, "lore-secret.git on branch main  (audience team:@secret)")
		tt.lore(tlAnn, "doctor").Says(t, "lore-secret (team:@secret)")
	})

	// Ann leaves the team: her next sync drops the store from her entry;
	// her clone stays on disk and is no longer read.
	w.Step("day3/ann-leaves-the-team-and-the-store-goes", func() {
		w.AdvanceDays(1)
		tt.lore(tlLee, "team", "member", "remove", "--handle", "@secret", "--members", "@ann", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		// D-727: the sync that drops the store says so once, with why
		// and what to do with the clone left on disk; never again
		tt.lore(tlAnn, "sync").OK(t).Says(t, "lore-secret: no longer read: audience team:@secret, you are not on it",
			"is left as it is: lore store forget --url ", " --yes removes it")
		tt.lore(tlAnn, "sync").OK(t).Never(t, "no longer read")
		tt.lore(tlAnn, "whoami").OK(t).Never(t, "no longer read")
		if cfg := asConfig(t, ann); strings.Contains(cfg, "lore-secret") {
			t.Fatalf("Ann's org entry still lists the @secret store after leaving the team:\n%s", cfg)
		}
		ann.Lore(filepath.Join(ann.Home, "code"), "search", "blue cluster").OK(t).Says(t, "(no matches)").Never(t, secretID, taSecretPhrase)
	})
}
