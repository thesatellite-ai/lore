//go:build journeys && !windows

package journeys

// join_secondstore_test.go — a new member joins with the url of one of the
// org's other stores, not its main store (devco D-721). `lore setup join
// --invite <second store url>` said "the store holds no org yet: the admin
// who made the org has not sent it", which was wrong: the org lives in its
// main store. `lore org store add` now writes the org's name and main store
// into the second store's lore-store.yaml, and the join follows it to the
// main store and joins the org (the second store comes with the org's
// entry, as the org lists it). A store that names no org gets a refusal
// that says lore cannot tell whose store it is and which url to join with,
// never "the admin has not sent it". An old store (listed before it named
// its org: here added while still an empty repo) is named by the admin's
// next lore sync (sent in that sync, said once), and then the join by its url works too.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJourneyJoinBySecondStore(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	url := tt.store.SSHURL()
	ops := w.Remote("github.com/acme-co/lore-ops")
	bare := w.Remote("github.com/acme-co/lore-bare")
	old := w.Remote("github.com/acme-co/lore-old")

	w.Step("day1/lee-starts-the-org-with-a-second-store", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, url, tlLee, []string{tlFolders})
		scratch := lee.Clone(w.Remote("github.com/acme-co/scratch"), "scratch")
		lee.LoreIn(scratch, "store", "init", "--name", "ops", "--url", ops.SSHURL(), "--yes").OK(t)
		lee.LoreIn(scratch, "store", "init", "--name", "bare", "--url", bare.SSHURL(), "--yes").OK(t)
		tt.lore(tlLee, "org", "store", "add", "--org", tlOrg, "--url", ops.SSHURL(), "--push", "--yes").OK(t).Says(t, "pushed", "names org "+tlOrg, "lore sync sends it")
		// an old store: listed while still an empty repo, so nothing named
		// the org in it, then made a lore store
		tt.lore(tlLee, "org", "store", "add", "--org", tlOrg, "--url", old.SSHURL(), "--push", "--yes").OK(t).Says(t, "no lore store yet")
		lee.LoreIn(scratch, "store", "init", "--name", "old", "--url", old.SSHURL(), "--yes").OK(t)
		if got, _ := old.Read("refs/heads/main", "lore-store.yaml"); strings.Contains(got, "org:") {
			t.Fatalf("the old store names an org before the admin's sync:\n%s", got)
		}
		tt.lore(tlLee, "member", "add", tlAnn.Email, "--name", tlAnn.Name, "--role", "member", "--yes").OK(t)
		// the admin's sync sends the added store's marker commit, and names
		// the org in the old store and sends that
		tt.lore(tlLee, "sync").OK(t).Says(t, "named org "+tlOrg, "lore-old")
		got, _ := ops.Read("refs/heads/main", "lore-store.yaml")
		for _, want := range []string{"org: " + tlOrg, "org_store: " + url} {
			if !strings.Contains(got, want) {
				t.Errorf("the second store's lore-store.yaml lacks %q:\n%s", want, got)
			}
		}
	})

	w.Step("day1/ann-joins-with-the-second-store-url", func() {
		ann := tt.person(tlAnn)
		dir := tt.repo[tlAnn.Email].Dir
		ann.Lore(dir, "setup", "join", "--invite", ops.SSHURL()).OK(t).
			Says(t, "a store of org "+tlOrg, url).
			Never(t, "holds no org yet", "has not sent it")
		var out struct {
			Commands []string `json:"commands"`
		}
		if err := json.Unmarshal([]byte(ann.Lore(dir, "setup", "join", "--invite", ops.SSHURL(), "--json").OK(t).Stdout), &out); err != nil || len(out.Commands) < 4 {
			t.Fatalf("setup join --json: commands %v (%v)", out.Commands, err)
		}
		for _, line := range out.Commands {
			ann.Lore(dir, append(joinLineArgs(t, line), "--yes")...).OK(t)
		}
		ann.Lore(dir, "setup", "list").OK(t).Says(t, tlOrg, "member")
		ann.Lore(dir, "org", "list").OK(t).Says(t, tlOrg)
		ann.Lore(dir, "store", "list").OK(t).Says(t, "lore-ops")
	})

	w.Step("day1/a-store-that-names-no-org-says-so", func() {
		bob := tt.person(tlBob)
		dir := tt.repo[tlBob.Email].Dir
		bob.Lore(dir, "setup", "join", "--invite", bare.SSHURL(), "--no-open").
			Refused(t, "lore cannot tell whose store", "names no org", "lore setup join --invite <the org's main store url>").
			Never(t, "holds no org yet", "has not sent it")
	})

	w.Step("day1/the-old-store-names-the-org-after-the-admins-sync-and-a-join-works", func() {
		got, _ := old.Read("refs/heads/main", "lore-store.yaml")
		if !strings.Contains(got, "org: "+tlOrg) || !strings.Contains(got, "org_store: "+url) {
			t.Fatalf("the admin's sync did not name the org in the old store:\n%s", got)
		}
		tt.lore(tlLee, "sync").OK(t).Never(t, "named org") // said once
		bob := tt.m[tlBob.Email]
		bob.Lore(tt.repo[tlBob.Email].Dir, "setup", "join", "--invite", old.SSHURL(), "--no-open").OK(t).
			Says(t, "a store of org "+tlOrg, `"invite":"`+url+`"`).
			Never(t, "cannot tell", "has not sent it")
	})
}
