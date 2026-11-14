//go:build journeys && !windows

package journeys

// identity_email_test.go — a person's email changes and the admin adds the
// new one as an identity contact (bug hunt G-14). Lee switches his org
// identity to the new email: whoami's identity line names the actor he has
// always been (the id his rows carry), never the id the new email would
// derive, which no row uses.

import (
	"strings"
	"testing"
)

func TestJourneyIdentityEmail(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	const newEmail = "lee.new@example.test"
	var leeID string

	w.Step("day1/start", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{"~/**"})
		tt.lore(tlLee, "init").OK(t)
		tt.lore(tlLee, "org", "link", tlOrg, "--project", tlProject, "--yes").OK(t)
		var who struct {
			Data struct {
				Identity struct {
					ActorID string `json:"actor_id"`
				} `json:"identity"`
			} `json:"data"`
		}
		tlJSON(t, tt.lore(tlLee, "whoami", "--json").OK(t), &who)
		if leeID = who.Data.Identity.ActorID; !strings.HasPrefix(leeID, "act_") {
			t.Fatalf("whoami --json actor id = %q", leeID)
		}
	})

	w.Step("month2/new-email-as-identity-contact", func() {
		tt.lore(tlLee, "actor", "contact", "add", "--actor", "@lee", "--type", "email", "--value", newEmail, "--identity", "--primary").OK(t)
		tt.lore(tlLee, "identity", "set", "--org", tlOrg, "--key", "human:"+newEmail, "--name", tlLee.Name, "--handle", "lee", "--yes").OK(t)
		tt.lore(tlLee, "whoami").OK(t).Says(t, "<human:"+newEmail+">   "+leeID, "is an identity contact of", "("+leeID+")")
		tt.lore(tlLee, "task", "add", "--title", "after the email change").OK(t)
		var tasks struct {
			Data []struct {
				Title     string `json:"title"`
				CreatedBy string `json:"created_by"`
			} `json:"data"`
		}
		tlJSON(t, tt.lore(tlLee, "task", "list", "--json").OK(t), &tasks)
		for _, x := range tasks.Data {
			if x.Title == "after the email change" && x.CreatedBy != leeID {
				t.Errorf("the task is by %s, whoami says %s", x.CreatedBy, leeID)
			}
		}
	})
}
