//go:build stories && !windows

package stories

// O_stale_fact_test.go — devco:D-941 (bug hunt H-190, owner feature F-2)
// on the owner-shaped machine (story O): the admin keeps notes about the
// platform repo's code in a folder of a personal store, each naming its
// evidence (fact_evidence: file:line, or a commit) and the day it was
// checked (fact_verified_at). A week later a file one note names changes.
// Nothing said that note may no longer hold. Now lint reports it with the
// changed file and the command that re-verifies it (which runs where
// printed and clears it), doctor counts it, and search and ask mark it and
// rank it below a fresh note with the same match, also when only a
// comment on it matched (with D-942).

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestStoryOStaleFactFlagged(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	commitOn := func(day string) {
		pc.With("GIT_AUTHOR_DATE="+day+"T10:00:00Z", "GIT_COMMITTER_DATE="+day+"T10:00:00Z").CommitAll("billing on " + day)
	}
	var fresh, stale, byCommit string

	s.Step("D-941/facts-with-evidence-in-a-store-folder", func() {
		pc.WriteFile("billing/refund.go", "package billing\n\n// Refund caps a refund at the order total.\nfunc Refund() {}\n")
		pc.WriteFile("billing/card.go", "package billing\n\n// Card refunds go back to the original card.\nfunc Card() {}\n")
		commitOn("2026-11-01")
		first := pc.Git("rev-parse", "--short", "HEAD")
		// the repo keeps its notes in a folder of the org store (no .lore folder)
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "billing").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "billing", "--own", "--yes").OK(t)
		fresh = oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--body", "quokka refunds says exports run nightly and take forty minutes",
			"--meta", "fact_evidence=billing/card.go:3", "--meta", "fact_verified_at=2026-11-02", "--json").OK(t))
		stale = oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--body", "quokka refunds says a refund is capped at the order total",
			"--meta", "fact_evidence=billing/refund.go:3", "--meta", "fact_verified_at=2026-11-02", "--json").OK(t))
		byCommit = oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--body", "Billing started in one commit with both payment paths.",
			"--meta", "fact_evidence="+first, "--json").OK(t))
		if fresh == "" || stale == "" || byCommit == "" {
			t.Fatal("an add --json printed no id")
		}
		ada.Lore(pc.Dir, "lint", "--code", "stale_fact").OK(t).Says(t, "lint: no findings")
	})

	s.Step("D-941/the-evidence-changes", func() {
		pc.WriteFile("billing/refund.go", "package billing\n\n// Refund no longer caps the amount.\nfunc Refund() {}\n")
		commitOn("2026-11-10")
	})

	var fix string
	s.Step("D-941/lint-and-doctor-report-it", func() {
		out := ada.Lore(pc.Dir, "lint", "--code", "stale_fact").OK(t).
			Says(t, "stale_fact "+stale, "changed after 2026-11-02: billing/refund.go (in ", "stale_fact "+byCommit, "2 finding(s)").Stdout
		if strings.Contains(out, "stale_fact "+fresh) {
			t.Fatalf("lint reports the fact whose evidence did not change:\n%s", out)
		}
		m := regexp.MustCompile(`lore meta set --id ` + stale + `[^;]*--pair fact_verified_at=\d{4}-\d{2}-\d{2}`).FindString(out)
		if m == "" || !strings.Contains(m, "--store ") {
			t.Fatalf("no re-verify command naming the row's store for %s:\n%s", stale, out)
		}
		fix = m
		ada.Lore(pc.Dir, "doctor").Says(t, "stale_fact: 2 row(s)", "lore lint --code stale_fact")
	})

	s.Step("D-941/search-and-ask-mark-it-below-the-fresh-row", func() {
		var sr struct {
			Data struct {
				Hits []struct {
					ID    string `json:"id"`
					Stale string `json:"stale"`
				} `json:"hits"`
			} `json:"data"`
		}
		raw := ada.Lore(pc.Dir, "search", "--query", "quokka refunds", "--json").OK(t).Stdout
		if err := json.Unmarshal([]byte(raw), &sr); err != nil {
			t.Fatalf("%v: %s", err, raw)
		}
		order := map[string]int{}
		for i, h := range sr.Data.Hits {
			order[h.ID] = i + 1
			if (h.ID == stale) != (h.Stale != "") {
				t.Fatalf("stale mark on the wrong hit: %s", raw)
			}
		}
		if order[fresh] == 0 || order[stale] == 0 || order[fresh] > order[stale] {
			t.Fatalf("search: want %s before %s:\n%s", fresh, stale, raw)
		}
		ada.Lore(pc.Dir, "search", "--query", "quokka refunds").OK(t).Says(t, "[stale: evidence changed since 2026-11-02 (billing/refund.go)]")
		ans := ada.Lore(pc.Dir, "ask", "--query", "quokka refunds", "--json").OK(t).Stdout
		var ar struct {
			Rows []struct {
				ID    string `json:"id"`
				Stale string `json:"stale"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(ans), &ar); err != nil {
			t.Fatalf("%v: %s", err, ans)
		}
		pos := map[string]int{}
		for i, r := range ar.Rows {
			pos[r.ID] = i + 1
			if (r.ID == stale) != (r.Stale != "") {
				t.Fatalf("ask: stale mark on the wrong row: %s", ans)
			}
		}
		if pos[fresh] == 0 || pos[stale] == 0 || pos[fresh] > pos[stale] {
			t.Fatalf("ask: want %s before %s:\n%s", fresh, stale, ans)
		}
	})

	// devco:D-941 with D-942: a comment on the stale note matches the
	// query and the note itself does not. The note is listed for its
	// comment (the comment under it, never above it) and still marked and
	// ranked as stale: a fresh note with the same words comes first.
	s.Step("D-941/a-comment-on-a-stale-row-keeps-it-stale", func() {
		fresh2 := oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--body", "walrus ledger: refunds post to the ledger the same day",
			"--meta", "fact_evidence=billing/card.go:3", "--meta", "fact_verified_at=2026-11-02", "--json").OK(t))
		ada.Lore(pc.Dir, "comment", "add", "--on-table", "memories", "--on-id", stale, "--body", "walrus ledger: refunds post to the ledger the same day").OK(t)
		var sr struct {
			Data struct {
				Hits []struct {
					ID         string `json:"id"`
					Stale      string `json:"stale"`
					ViaComment string `json:"via_comment"`
					CommentOn  struct {
						ID string `json:"id"`
					} `json:"comment_on"`
				} `json:"hits"`
			} `json:"data"`
		}
		raw := ada.Lore(pc.Dir, "search", "--query", "walrus ledger", "--json").OK(t).Stdout
		if err := json.Unmarshal([]byte(raw), &sr); err != nil {
			t.Fatalf("%v: %s", err, raw)
		}
		hits := sr.Data.Hits
		at := -1
		for i, h := range hits {
			if h.ID == stale {
				at = i
			}
		}
		if at < 1 || hits[0].ID != fresh2 {
			t.Fatalf("want the fresh note %s first, then the stale note %s listed for its comment:\n%s", fresh2, stale, raw)
		}
		if hits[at].Stale == "" || hits[at].ViaComment == "" {
			t.Fatalf("the stale note listed for its comment is not marked stale:\n%s", raw)
		}
		if at+1 >= len(hits) || hits[at+1].CommentOn.ID != stale {
			t.Fatalf("the comment is not directly under its row:\n%s", raw)
		}
	})

	s.Step("D-941/the-printed-fix-runs-and-clears-it", func() {
		ada.Lore(pc.Dir, strings.Fields(strings.TrimPrefix(fix, "lore "))...).OK(t)
		out := ada.Lore(pc.Dir, "lint", "--code", "stale_fact").OK(t).Says(t, "1 finding(s)", "stale_fact "+byCommit).Stdout
		if strings.Contains(out, "stale_fact "+stale) {
			t.Fatalf("the re-verified fact is still reported:\n%s", out)
		}
	})
}
