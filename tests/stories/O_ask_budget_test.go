//go:build stories && !windows

package stories

// O_ask_budget_test.go — devco D-1013 (bug hunt H-275) on the
// owner-shaped machine (story O): three as-built notes of about 2,000
// characters each answer one question, each one part of it. lore ask's
// default --max-chars was 4,000, so one note printed whole and the other
// two were cut to their best section or listed as candidate lines, and
// the reader needed a lore show for their answering sentences. The
// default is now 8,000: with no --max-chars all three print whole.

import (
	"fmt"
	"strings"
	"testing"
)

func TestStoryOAskDefaultBudgetPrintsThreeRows(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	parts := []struct{ title, head, text, answer string }{
		{"As built: parcel gateway", "Gateway drain", "The parcel gateway drains each tenant before the rollout moves on.", "Drained tenants are logged to the dock journal."},
		{"As built: parcel queue", "Queue replay", "The parcel queue replays every held message after the rollout.", "Replays stop after the fifth failed attempt."},
		{"As built: parcel cache", "Cache eviction", "The parcel cache evicts each shard once the rollout ends.", "Evicted shards are warmed again by the night job."},
	}
	pad := []string{
		"The parcel desk prints labels at the dock and carriers collect parcels twice daily.",
		"Shipping zones map postcodes to carriers; a missing postcode falls back to the national zone.",
		"Metrics are scraped every fifteen seconds and dashboards keep thirty days of history.",
		"Translations live in locale files and the fallback locale is the first storefront's.",
	}

	s.Step("D-1013/three-long-notes", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "parcels").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "parcels", "--own", "--yes").OK(t)
		for _, p := range parts {
			var b strings.Builder
			fmt.Fprintf(&b, "# %s\n\n## %s\n\n%s\n\n## Follow-up\n\n%s\n\n", p.title, p.head, p.text, p.answer)
			for n := 0; b.Len() < 2000; n++ {
				fmt.Fprintf(&b, "## Notes %d\n\n%s %s\n\n", n+1, pad[n%len(pad)], pad[(n+1)%len(pad)])
			}
			ada.Lore(pc.Dir, "architecturenote", "add", "--title", p.title, "--body", b.String(), "--meta", "fact_state=live", "--yes").OK(t)
		}
		for i, w := range []string{"zones", "carriers", "scales", "pallets", "returns", "rates", "hours", "badges"} {
			ada.Lore(pc.Dir, "memory", "add", "--body", fmt.Sprintf("At the parcel desk the %s list is tab %d of the shared sheet.", w, i+1), "--yes").OK(t)
		}
	})

	s.Step("D-1013/the-default-prints-all-three-whole", func() {
		q := "how does the parcel rollout handle the gateway drain, the queue replay and the cache eviction"
		r := ada.Lore(pc.Dir, "ask", "--query", q).OK(t)
		for _, p := range parts {
			r.Says(t, p.answer)
		}
		if out := r.Out(); strings.Contains(out, "prints it whole") {
			t.Errorf("[O D-1013] a note was printed cut at the default --max-chars:\n%s", r)
		}
		// the old cap of 4,000 cannot hold the three whole: fewer than
		// three notes are printed (a note up to 2,500 characters is never
		// cut since devco D-1028: one that does not fit is a candidate line)
		cut := ada.Lore(pc.Dir, "ask", "--query", q, "--max-chars", "4000").OK(t).Out()
		notes := 0
		for l := range strings.SplitSeq(cut, "\n") {
			if rest, ok := strings.CutPrefix(l, "cited: "); ok {
				for _, id := range strings.Fields(rest) {
					if strings.HasPrefix(id, "anr_") {
						notes++
					}
				}
			}
		}
		if notes >= len(parts) {
			t.Errorf("[O D-1013] at --max-chars 4000 the three notes printed whole; the fixture no longer needs the larger default:\n%s", cut)
		}
	})
}
