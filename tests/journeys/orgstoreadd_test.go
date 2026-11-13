//go:build journeys && !windows

package journeys

// orgstoreadd_test.go — a lead adds a second store to the org for one
// client's notes (B-4, the contractor layout). Before `lore org store add`
// the store had to be hand-written into lore-org.yaml in the store clone
// and committed and pushed with git, and `lore org link --store` refused
// naming only the team store. Now the refusal names the command; the
// command commits lore-org.yaml in the store clone without pushing, the
// lead's entry lists the store at once, and --push sends it to the team.

import (
	"strings"
	"testing"
)

func TestJourneyOrgStoreAdd(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	client := w.Remote("github.com/acme-co/client-store")
	orgFile := "org/lore-org.yaml"

	w.Step("day1/team-and-a-client-store", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		// a plain git folder of the lead's, where the store is made
		scratch := lee.Clone(w.Remote("github.com/acme-co/scratch"), "scratch")
		lee.LoreIn(scratch, "store", "init", "--name", "client", "--url", client.SSHURL(), "--yes").OK(t)
		tt.lore(tlLee, "init", "--non-interactive", "--name=app").OK(t)
	})

	w.Step("day1/link-refuses-and-names-the-command", func() {
		tt.lore(tlLee, "org", "link", "--org", tlOrg, "--project", "bigco", "--store", client.SSHURL(), "--yes").
			Refused(t, "is not one of "+tlOrg+"'s stores", "lore org store add --org "+tlOrg+" --url "+client.SSHURL())
	})

	before := ""
	w.Step("day1/add-the-store-without-pushing", func() {
		before, _ = tt.store.Read("refs/heads/main", orgFile)
		tt.lore(tlLee, "org", "store", "add", "--org", tlOrg, "--url", client.SSHURL(), "--yes").OK(t).
			Says(t, "added store", "not pushed", "org entry "+tlOrg+" lists it")
		if got, _ := tt.store.Read("refs/heads/main", orgFile); got != before {
			t.Fatalf("the team store changed without --push:\n%s", got)
		}
		tt.lore(tlLee, "org", "link", "--org", tlOrg, "--project", "bigco", "--store", client.SSHURL(), "--yes").OK(t).Says(t, "linked")
	})

	w.Step("day1/push-it-to-the-team", func() {
		tt.lore(tlLee, "org", "store", "add", "--org", tlOrg, "--url", client.SSHURL(), "--push", "--yes").OK(t).Says(t, "pushed")
		got, _ := tt.store.Read("refs/heads/main", orgFile)
		if !strings.Contains(got, "client-store") || !strings.Contains(got, "team-store") && !strings.Contains(got, tt.store.SSHURL()) {
			t.Fatalf("pushed lore-org.yaml lacks a store:\n%s", got)
		}
		tt.lore(tlLee, "org", "store", "add", "--org", tlOrg, "--url", client.SSHURL(), "--yes").OK(t).Says(t, "already lists it")
	})
}
