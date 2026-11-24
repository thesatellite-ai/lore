//go:build stories && !windows

package stories

// O_ask_asbuilt_test.go — devco D-963 (bug hunt H-222, Sync benchmark set
// 4 item 1) on the owner-shaped machine (story O): an own repo of the org
// linked to its folder keeps as-built architecture notes there (one of
// them live and verified, one planned memory), the company folder org/
// holds ten code_repo resources whose purpose repeats the question's
// words, and another client's folder holds notes sharing a few words. The
// answer filled its 4,000 characters with the ten cards and the other
// client's rows and never printed the architecture note (ask read no
// architecture notes). Now the note leads, the cards print one short line
// each within a fifth of the cap, the repo's own rows come before the
// other client's (ask --store <org>, which reads every folder), and the
// planned note comes last. devco D-968 (H-227, Sync benchmark set 5):
// such resources take one item of the answer (not a fifth of the cap),
// after every row, so the hotfix that answers a product question is shown.

import (
	"fmt"
	"strings"
	"testing"
)

func TestStoryOAskAsBuiltNotesFirst(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	home, company, other := oOrg+"/feedapp", oOrg+"/org", oOrg+"/clientb"

	s.Step("D-963/as-built-notes-cards-and-another-client", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "feedapp").OK(t)
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "clientb").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "feedapp", "--own", "--yes").OK(t)
		ada.Lore(pc.Dir, "resource", "kind", "add", "--kind", "code_repo", "--to", company, "--yes").OK(t)
		for _, f := range []string{"purpose", "stack", "url"} {
			ada.Lore(pc.Dir, "resource", "field", "add", "--kind", "code_repo", "--field", f, "--type", "text", "--to", company, "--yes").OK(t)
		}
		for i := range 10 {
			n := fmt.Sprintf("feed_part%d", i)
			ada.Lore(pc.Dir, "resource", "add", "--kind", "code_repo", "--name", n,
				"--set", "purpose=Part of the feed product: job queue workers, retry handling, import and export feeds, admin screens, public URLs and webhooks",
				"--set", "stack=Go, Postgres, a job queue, a worker pool, retries with backoff, NATS, Redis, React admin",
				"--set", "url=https://git.example.test/"+n, "--to", company, "--yes").OK(t)
		}
		ada.Lore(pc.Dir, "memory", "add", "--body", "Plan: the feed import pipeline moves to one streaming step someday.", "--meta", "fact_state=planned", "--yes").OK(t)
		ada.Lore(pc.Dir, "architecturenote", "add", "--title", "As built: feed import pipeline",
			"--body", "As built: the feed import runs in two steps: fetch the bulk file into object storage, then parse_and_ingest writes rows in batches of 500 through the one writer.",
			"--meta", "fact_state=live", "--meta", "fact_verified_at=2026-11-20", "--yes").OK(t)
		ada.Lore(pc.Dir, "memory", "add", "--body", "Client B: their feed import pipeline runs nightly.", "--to", other, "--yes").OK(t)
		ada.Lore(pc.Dir, "whoami").OK(t).Says(t, home)
	})

	s.Step("D-963/the-as-built-note-leads", func() {
		r := ada.Lore(pc.Dir, "ask", "--query", "how does the feed import pipeline work").OK(t)
		out := r.Out()
		note, plan, client := strings.Index(out, "As built: feed import pipeline"), strings.Index(out, "streaming step"), strings.Index(out, "Client B")
		card := strings.Index(out, "code_repo ")
		if note < 0 || card >= 0 && card < note || client >= 0 && client < note || plan >= 0 && plan < note {
			t.Fatalf("[O D-963] `lore ask` does not lead with the as-built note:\n%s", r)
		}
		if client >= 0 && plan >= 0 && plan < client {
			t.Errorf("[O D-963] the planned note ranks above another client's row with the same words:\n%s", r)
		}
		cards := 0
		for l := range strings.SplitSeq(out, "\n") {
			if strings.HasPrefix(l, "code_repo ") {
				cards++
				if strings.Contains(l, "stack") || len(l) > 220 {
					t.Errorf("[O D-963] a card the question only touches prints more than one short line: %q", l)
				}
			}
		}
		// devco D-968: one item at the default cap, not a fifth of it
		if cards > 1 || !strings.Contains(out, "more resources that only share a word with the question left out; --max-chars 8000 prints more") {
			t.Errorf("[O D-963/D-968] %d cards; want at most one and the line saying the rest were left out:\n%s", cards, r)
		}
		// the printed next step runs where printed and prints more cards
		more := ada.Lore(pc.Dir, "ask", "--query", "how does the feed import pipeline work", "--max-chars", "8000").OK(t)
		if strings.Count(more.Out(), "\ncode_repo ") <= cards {
			t.Errorf("[O D-963] --max-chars 8000 prints no more cards:\n%s", more)
		}
	})

	s.Step("D-968/resources-named-after-the-product-take-one-item", func() {
		// Sync benchmark set 5 (S5-15): code_repo resources named after the
		// product and its integration took four to six items and the
		// hotfix that answered was left out
		for _, n := range []string{"shop_app", "shop_import_worker", "sync_shop", "pim_shop_bridge", "workflow_engine", "workspace_api"} {
			ada.Lore(pc.Dir, "resource", "add", "--kind", "code_repo", "--name", n,
				"--set", "purpose=Shop and PIM integration: imports products, variants and options from a shop workspace into the catalog, per workflow",
				"--set", "url=https://git.example.test/"+n, "--to", company, "--yes").OK(t)
		}
		ada.Lore(pc.Dir, "rule", "add", "--body", "Every shop import records who started it (workflow and user) on its run.", "--yes").OK(t)
		ada.Lore(pc.Dir, "hotfix", "add", "--title", "One shop import per workflow and workspace",
			"--body", "Only one shop import may run per workflow and workspace at a time: a second start returns ErrImportRunning.", "--yes").OK(t)
		q := "can two shop imports run at the same time for the same workflow and workspace"
		r := ada.Lore(pc.Dir, "ask", "--query", q).OK(t)
		out := r.Out()
		hotfix, card := strings.Index(out, "One shop import per workflow and workspace"), strings.Index(out, "code_repo ")
		if hotfix < 0 || card >= 0 && card < hotfix || strings.Count(out, "\ncode_repo ") > 1 {
			t.Errorf("[O D-968] the hotfix is not shown before at most one code_repo line:\n%s", r)
		}
		// the printed next step runs where printed and prints one more
		more := ada.Lore(pc.Dir, "ask", "--query", q, "--max-chars", "8000").OK(t)
		if strings.Count(more.Out(), "\ncode_repo ") != 2 {
			t.Errorf("[O D-968] --max-chars 8000 does not print one more code_repo line:\n%s", more)
		}
		// named whole: the question is about it and its card stays
		named := ada.Lore(pc.Dir, "ask", "--query", "what does shop_import_worker do").OK(t)
		named.Says(t, "shop_import_worker")
	})

	s.Step("D-963/the-repo-folder-before-another-client", func() {
		// the other client's row is the shorter, closer text match: only
		// the folder puts the repo's own row first
		ada.Lore(pc.Dir, "memory", "add", "--body", "The webhook retry waits thirty seconds between tries, then gives up after the fifth try and alerts the admin on call by email and chat.", "--yes").OK(t)
		ada.Lore(pc.Dir, "memory", "add", "--body", "Client B: webhook retry waits five seconds.", "--to", other, "--yes").OK(t)
		// --store <org> reads every folder of the store, the other
		// client's too (the linked repo alone reads its own folder)
		out := ada.Lore(pc.Dir, "ask", "--store", oOrg, "--query", "how long does the webhook retry wait").OK(t).Out()
		own, cb := strings.Index(out, "thirty seconds"), strings.Index(out, "five seconds")
		if own < 0 || cb >= 0 && cb < own {
			t.Errorf("[O D-963] the other client's row ranks above the repo's own (%s):\n%s", home, out)
		}
	})
}
