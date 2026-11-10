//go:build teame2e

package teame2e

// later.md 229 (clientrepo, T-TEAME2E): the repo's role (client or own) as
// cmd/lore decides it (wire.OrgResolution.RepoIsClient, fail closed), proved
// by what a user sees: the whoami "repo" line, the F16 leak guard on a repo
// write carrying internal text, the F18 actor-row projection (no email in a
// client repo's files), and the fail-closed answers when the org store
// cannot be read.

import (
	"os"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teDInternalNote is internal text (14 words); a run of 8 of its words in a
// client-readable row is a leak (F16).
const teDInternalNote = "the client is slow to pay so always send the estimate before every call"

// teDRepoRole is the whoami "repo" line verdicts.
const (
	teDRoleClient = "client repo"
	teDRoleOwn    = "own repo"
)

// teDRepoLine returns the whoami "repo" line of c.
func teDRepoLine(t *testing.T, c *th.Clone) string {
	t.Helper()
	r := c.MustLore("whoami")
	for _, l := range strings.Split(r.Stdout, "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == "repo" {
			return l
		}
	}
	t.Fatalf("whoami has no repo line: %s", r)
	return ""
}

// teDRepoRules counts the rule row files in c's work tree data folder (the
// guards these tests check are on --to repo rule writes).
func teDRepoRules(t *testing.T, c *th.Clone) int {
	t.Helper()
	ents, err := os.ReadDir(c.Dir + "/" + dataDir + "/" + tblRules)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(ents)
}

// The role matrix on one laptop (alice, §5.2 config): each repo kind gets the
// role F10 gives it, and the role switches the guards a user meets.
//
//	repo                                  role    internal text --to repo   actor row email in repo
//	no org (someone/tool)                 none    written (nothing internal) never
//	own pattern + reachable store         own     written                   yes (Q-R2-2)
//	repos: pattern (acme/app)             client  E_LEAK_BLOCKED, nothing   never
//	non-own link (someone/tool linked)    client  E_LEAK_BLOCKED, nothing   never
//	staging entry chosen on purpose       client  (whoami)                  -
func TestTeD_RepoRoleMatrix(t *testing.T) {
	o := newOrgWorld(t, th.Cmd("org", "use"), th.Cmd("org", "link"), th.Flag("to", "rule", "add"), th.Flag("to", "memory", "add"))
	// The internal note lives in the org folder, which every repo of the
	// org attaches whatever its link folder.
	o.tools.MustLore("memory", "add", "--body", teDInternalNote, "--to", orgNorthwind+"/"+folderOrg)

	// No org: no client guards, nothing internal attached, no email in files.
	if l := teDRepoLine(t, o.loose); strings.Contains(l, teDRoleClient) || strings.Contains(l, teDRoleOwn) {
		t.Fatalf("a repo no org matches is neither client nor own: %q", l)
	}
	o.loose.MustLore("rule", "add", "--body", "keep the changelog short", "--to", "repo")

	// Own repo (own pattern, store reachable): internal text may live in
	// the repo's rows, and the actor row keeps the org key (Q-R2-2).
	if l := teDRepoLine(t, o.tools); !strings.Contains(l, teDRoleOwn) {
		t.Fatalf("own pattern + reachable store is an own repo: %q", l)
	}
	o.tools.MustLore("rule", "add", "--body", teDInternalNote, "--to", "repo")
	if f := o.tools.LeakScan(aliceKey); len(f) == 0 {
		t.Fatal("control: an own repo's actor row carries the org identity key (Q-R2-2); the scan below proves nothing without it")
	}

	// Client repo by the repos: pattern: the same write is refused and
	// nothing reaches the work tree; the actor row is the F18 projection.
	if l := teDRepoLine(t, o.acme); !strings.Contains(l, teDRoleClient) {
		t.Fatalf("repos: pattern is a client repo: %q", l)
	}
	before := teDRepoRules(t, o.acme)
	o.acme.Lore("rule", "add", "--body", teDInternalNote, "--to", "repo").Fails(t, th.ExitGuard, "E_LEAK_BLOCKED", "Nothing written")
	if n := teDRepoRules(t, o.acme); n != before {
		t.Fatalf("a blocked write leaves no rule file: %d -> %d", before, n)
	}
	o.acme.MustLore("rule", "add", "--body", "money is stored in minor units", "--to", "repo")
	if f := o.acme.LeakScan(aliceKey, teDInternalNote); len(f) != 0 {
		t.Fatalf("a client repo's checkout holds the org key or internal text: %v", f)
	}

	// A link from the org's store makes a repo no pattern names a client
	// repo of that org for everyone (non-own link).
	o.loose.With(th.EnvLoreOrg+"="+orgNorthwind).MustLore("org", "link", orgNorthwind, "--project", "loose", "--yes")
	if l := teDRepoLine(t, o.loose); !strings.Contains(l, teDRoleClient) {
		t.Fatalf("a non-own link makes a client repo: %q", l)
	}
	o.loose.Lore("rule", "add", "--body", teDInternalNote, "--to", "repo").Fails(t, th.ExitGuard, "E_LEAK_BLOCKED")
	if f := o.loose.LeakScan(teDInternalNote); len(f) != 0 {
		t.Fatalf("the linked repo holds internal text: %v", f)
	}

	// The staging entry chosen on purpose for a client repo keeps it a
	// client repo (its store branch has no link for it: still client).
	o.acme.MustLore("org", "use", orgStaging)
	if l := teDRepoLine(t, o.acme); !strings.Contains(l, teDRoleClient) {
		t.Fatalf("a staging entry over a client repo is still a client repo: %q", l)
	}
	o.acme.MustLore("org", "use", orgNorthwind)
}

// Fail closed when the org store cannot be read (no local copy, offline):
// an own repo still refuses a write with no --to (E_LINK_UNKNOWN, it cannot
// tell where the write belongs) but takes an explicit --to repo; a client
// repo refuses even an explicit --to repo with plain text, because it
// cannot check the write for internal content (E_LEAK_BLOCKED).
func TestTeD_RepoRoleUnreachableStoreFailsClosed(t *testing.T) {
	o := newOrgWorld(t, th.Flag("to", "rule", "add"), th.Flag("to", "memory", "add"))
	o.tools.MustLore("memory", "add", "--body", teDInternalNote, "--to", orgNorthwind+"/"+folderOrg)
	for _, dir := range []string{"clones", "cache"} {
		if err := os.RemoveAll(o.alice.LorePath(dir)); err != nil {
			t.Fatal(err)
		}
	}
	o.alice.Offline()

	o.tools.Lore("rule", "add", "--body", "plain words with no target").Fails(t, th.ExitLinkUnknown, "E_LINK_UNKNOWN", "--to")
	o.tools.MustLore("rule", "add", "--body", "plain words for the repo", "--to", "repo")

	before := teDRepoRules(t, o.acme)
	for _, body := range []string{"plain words for the client repo", teDInternalNote} {
		o.acme.Lore("rule", "add", "--body", body, "--to", "repo").Fails(t, th.ExitGuard, "E_LEAK_BLOCKED", "cannot check")
	}
	if n := teDRepoRules(t, o.acme); n != before {
		t.Fatalf("a write that cannot be checked writes nothing: %d -> %d", before, n)
	}
	// Reads keep working.
	o.acme.MustLore("rule", "list")

	// Back online, the store is cloned again and the plain write goes in.
	o.alice.Online()
	o.acme.MustLore("rule", "add", "--body", "plain words for the client repo", "--to", "repo")
	o.acme.Lore("rule", "add", "--body", teDInternalNote, "--to", "repo").Fails(t, th.ExitGuard, "E_LEAK_BLOCKED")
}
