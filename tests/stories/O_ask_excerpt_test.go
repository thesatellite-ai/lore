//go:build stories && !windows

package stories

// O_ask_excerpt_test.go — devco D-970 (bug hunt H-229, Sync benchmark set
// 5 ask 3) on the owner-shaped machine (story O): an own repo of the org
// linked to its folder keeps a long as-built architecture note whose
// "Write path" section repeats the question's common words in three
// paragraphs and names the only caller of the asked function in one
// sentence after them. lore ask cut the section to the paragraphs with
// the common words and dropped that sentence; now the cut keeps it under
// its heading path, and a decision cut at --max-chars keeps the sentence
// naming the asked error.

import (
	"strings"
	"testing"
)

func TestStoryOAskExcerptKeepsAskedSentence(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	filler := strings.Repeat("The stock sync of each warehouse reads the inbox in batches and writes the stock table per workspace, warehouse after warehouse. ", 3)
	caller := "The only caller of applyLevelDelta is the nightly reconcile job in jobs/reconcile.go."

	s.Step("D-970/a-long-note-and-a-decision", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "stockapp").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "stockapp", "--own", "--yes").OK(t)
		note := "# As built: warehouse stock sync\n\n## Overview\n\n" + filler + "\n\n## Write path\n\n" + filler + "\n\n" + filler + "\n\n" + filler + "\n\nBatches are claimed with a two-minute lease. " + caller + " Leases are swept hourly.\n\n## Operations\n\n" + filler
		ada.Lore(pc.Dir, "architecturenote", "add", "--title", "As built: warehouse stock sync", "--body", note, "--meta", "fact_state=live", "--yes").OK(t)
		ada.Lore(pc.Dir, "decision", "add", "--title", "Order export retries whole batches",
			"--body", "The order export sends orders in batches of 100 and a batch lands whole or not at all. A failed order export batch is retried with backoff up to five times. Each batch reads from a snapshot taken when it starts; a snapshot older than the newest change ends the batch with ErrStaleSnapshot. Operators see parked order export batches on the dashboard.", "--yes").OK(t)
	})

	s.Step("D-970/the-cut-keeps-the-asked-sentence", func() {
		r := ada.Lore(pc.Dir, "ask", "--query", "who calls applyLevelDelta in the warehouse stock sync").OK(t)
		out := r.Out()
		if !strings.Contains(out, caller) || !strings.Contains(out, "Write path") {
			t.Fatalf("[O D-970] the cut note dropped the sentence naming the asked function or its heading:\n%s", r)
		}
		if !strings.Contains(out, "lore show --id") {
			t.Errorf("[O D-970] the note was not cut (the step needs a cut):\n%s", r)
		}
		r = ada.Lore(pc.Dir, "ask", "--query", "what does the order export do on ErrStaleSnapshot", "--max-chars", "300").OK(t)
		if !strings.Contains(r.Out(), "ends the batch with ErrStaleSnapshot") {
			t.Errorf("[O D-970] the cut decision dropped the sentence naming the asked error:\n%s", r)
		}
		// the printed next step runs where printed and prints the row whole
		id := ""
		for l := range strings.SplitSeq(r.Out(), "\n") {
			if i := strings.Index(l, "lore show --id "); i >= 0 && strings.Contains(l, "prints it whole") {
				id = strings.Fields(l[i+len("lore show --id "):])[0]
			}
		}
		if id == "" {
			t.Fatalf("[O D-970] no lore show --id line under the cut decision:\n%s", r)
		}
		ada.Lore(pc.Dir, "show", "--id", id).OK(t).Says(t, "Operators see parked order export batches")
	})
}
