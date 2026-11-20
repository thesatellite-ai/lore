//go:build journeys && !windows

package journeys

// orgminlore_test.go — an org declares the lore version its stores need
// (devco:D-735). Lee's org keeps a client's notes in a store for the
// @secret team; a member on an older lore skips such a store without a
// word (devco:D-734). Setting the team audience now offers the org floor;
// Lee declares min_lore_version 0.1.20 with `lore org config set`. Ann's
// lore stamped 0.1.19 (what an older release behaves like: every lore
// since 0.1.18 checks a folder's min_lore_version before it writes) still
// reads the org's notes but every write is refused with the version it
// needs and `lore upgrade`; the current lore writes as before.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	// omLateFolder is a folder created after the org declared its floor,
	// omLateBody the admin's note in it (devco:D-735).
	omLateFolder = "ops"
	omLateBody   = "late folder runbook"

	omBody     = "staging resets every sunday night"
	omOldBody  = "an old lore must not write this"
	omNewBody  = "the current lore writes this"
	omOldStamp = "0.1.19"
)

var (
	stampedOnce sync.Once
	stampedPath string
	stampedErr  string
)

// stampedLore is this tree's lore built with version omOldStamp (an old
// client by its version: the format gate compares the stamp).
func stampedLore(t testing.TB) string {
	t.Helper()
	stampedOnce.Do(func() {
		dir, err := os.MkdirTemp("", "journeys-stamped-")
		if err != nil {
			stampedErr = err.Error()
			return
		}
		stampedPath = filepath.Join(dir, "lore")
		c := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-tags", "loretest", "-ldflags", "-X main.version="+omOldStamp, "-o", stampedPath, "./cmd/lore")
		c.Dir = filepath.Join("..", "..")
		c.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := c.CombinedOutput(); err != nil {
			stampedErr = err.Error() + ": " + string(out)
		}
	})
	if stampedErr != "" {
		t.Fatalf("build lore %s: %s", omOldStamp, stampedErr)
	}
	return stampedPath
}

// withBin runs bin (instead of the lore under test) in clone c.
func withBin(t testing.TB, bin string, c *teamharness.Clone, args ...string) teamharness.Result {
	t.Helper()
	cmd := c.Command(args...)
	cmd.Path, cmd.Args[0] = bin, bin
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	r := teamharness.Result{Args: args, Stdout: so.String(), Stderr: se.String()}
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("lore %s: %v", strings.Join(args, " "), err)
		}
		r.Code = ee.ExitCode()
	}
	return r
}

func TestJourneyOrgMinLoreVersion(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	sec := w.Remote(taSecretSlug)
	line := ""
	old := stampedLore(t)

	w.Step("day1/lee-starts-the-org-and-ann-joins", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "init", "--non-interactive", "--name=app").OK(t)
		c := tt.repo[tlLee.Email]
		c.CommitAll("lore init")
		c.Git("push", "-q", "origin", "HEAD")
		tt.lore(tlLee, "org", "link", "--org", tlOrg, "--project", tlProject, "--yes").OK(t)
		tt.lore(tlLee, "memory", "add", "--body", omBody, "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		_, line = tlInvite(t, lee, c.Dir, tlAnn.Email)
	})

	ann := tt.person(tlAnn)
	w.Step("day1/ann-joins", func() {
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		tt.repo[tlAnn.Email].Git("pull", "-q", "--no-rebase", "origin", "main")
		tt.lore(tlAnn, "sync").OK(t)
		tt.lore(tlAnn, "search", "staging resets").OK(t).Says(t, omBody)
	})

	// a team audience says what an older member's lore does and offers
	// the floor; it never sets it
	w.Step("day2/a-team-store-offers-the-floor", func() {
		w.AdvanceDays(1)
		tt.lore(tlLee, "team", "add", "--handle", "@secret", "--members", "@lee", "--yes").OK(t)
		// Ann works in the platform folder (a member writes a team's folder)
		tt.lore(tlLee, "team", "add", "--handle", "@"+tlProject, "--members", "@ann", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		tt.lore(tlLee, "org", "store", "add", "--org", tlOrg, "--url", sec.SSHURL(), "--audience", "team:@secret", "--push", "--yes").OK(t).
			Says(t, "a member on lore older than 0.1.19 skips a team store without a word",
				"lore org config set --org "+tlOrg+" --key min_lore_version --value 0.1.19 --push --yes")
		tt.lore(tlLee, "org", "config", "get", "--org", tlOrg, "--key", "min_lore_version").OK(t).Says(t, "min_lore_version: 0.1.18") // the format features of 0.1.18 rows
	})

	w.Step("day2/only-an-admin-declares-it-and-never-above-their-own-lore", func() {
		tt.lore(tlAnn, "org", "config", "set", "--org", tlOrg, "--key", "min_lore_version", "--value", "0.1.20", "--yes").
			Refused(t, "only an admin of "+tlOrg)
		tt.lore(tlLee, "org", "config", "set", "--org", tlOrg, "--key", "min_lore_version", "--value", "0.1.99", "--yes").
			Refused(t, "below 0.1.99", "would turn read-only on "+tlOrg+"'s stores itself", "lore upgrade")
		tt.lore(tlLee, "org", "config", "set", "--org", tlOrg, "--key", "min_lore_version", "--value", "soon", "--yes").
			Refused(t, "is not a release version")
	})

	w.Step("day2/lee-declares-0.1.20", func() {
		tt.lore(tlLee, "org", "config", "set", "--org", tlOrg, "--key", "min_lore_version", "--value", "0.1.20", "--push", "--yes").OK(t).
			Says(t, "now need lore 0.1.20", "(pushed)")
		tt.lore(tlLee, "org", "config", "get", "--org", tlOrg, "--key", "min_lore_version").OK(t).Says(t, "min_lore_version: 0.1.20")
		// the floor is only ever raised: a lower one changes nothing
		tt.lore(tlLee, "org", "config", "set", "--org", tlOrg, "--key", "min_lore_version", "--value", "0.1.19", "--push", "--yes").OK(t).
			Says(t, "already needs 0.1.20; kept").Never(t, "now need lore 0.1.19")
		// the admin's own (current) lore keeps writing
		tt.lore(tlLee, "memory", "add", "--body", "lee still writes", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
	})

	// devco:D-735: a folder made after the declaration (the floor lives in
	// lore-org.yaml and the org entry, not only in the folders that
	// existed) declares it too
	w.Step("day2/a-folder-made-after-the-declaration-needs-it-too", func() {
		tt.lore(tlLee, "team", "add", "--handle", "@"+omLateFolder, "--members", "@ann", "--yes").OK(t) // ann may write there
		tt.lore(tlLee, "store", "folder", "add", "--store", "team", "--folder", omLateFolder).OK(t)
		tt.lore(tlLee, "memory", "add", "--body", omLateBody, "--to", tlOrg+"/"+omLateFolder, "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
	})

	w.Step("day3/ann-on-an-old-lore-reads-but-cannot-write", func() {
		w.AdvanceDays(1)
		c := tt.repo[tlAnn.Email]
		tt.lore(tlAnn, "sync").OK(t) // the roster with @platform, before her lore is "old"
		withBin(t, old, c, "sync")
		withBin(t, old, c, "search", "staging resets").OK(t).Says(t, omBody)
		withBin(t, old, c, "memory", "add", "--body", omOldBody, "--yes").
			Refused(t, "needs lore 0.1.20 or later to write (this is "+omOldStamp+")", "Reading works", "lore upgrade")
		tt.lore(tlAnn, "search", "must not write").OK(t).Never(t, omOldBody)
		// the folder made after the declaration is read-only for her too
		withBin(t, old, c, "memory", "add", "--body", omOldBody, "--to", tlOrg+"/"+omLateFolder, "--yes").
			Refused(t, "needs lore 0.1.20 or later to write (this is "+omOldStamp+")")
	})

	w.Step("day3/ann-upgrades-and-writes", func() {
		tt.lore(tlAnn, "sync").OK(t)
		tt.lore(tlAnn, "memory", "add", "--body", omNewBody, "--yes").OK(t)
		tt.lore(tlAnn, "sync").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		tt.lore(tlLee, "search", "current lore writes").OK(t).Says(t, omNewBody)
	})
}
