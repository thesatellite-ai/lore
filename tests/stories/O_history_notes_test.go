//go:build stories && !windows

package stories

// O_history_notes_test.go — devco:D-994 (bug hunt H-262, owner org
// questions (a)) on the owner-shaped machine (story O): before handing an
// own repo to a client, the admin asks whether an internal note was ever
// committed to it. --as-client read only the current files and --history
// only secrets, so a note pasted into a file past the hooks and removed
// later was reported by neither, while the client would receive every
// commit. `lore check leaks --history --as-client` now applies the
// hand-over rules to every file version and commit message, and plain
// --history applies the commit hook's rules in a client repo; each finding
// names the commit that added it, and the printed `lore leaks allow` line
// runs where printed and clears it.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestStoryOHistoryNoteScan(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	app := s.Seed(s.Remote("github.com/acme-client/client-app"), func(dir string) { PlainRepo(t, dir) })
	const note = "The client pays forty percent below list until the spring pricing review is over."

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ac := ada.CloneTo(app, ada.OrgDir("client-app"))
	var noteID string
	pastHooks := func(c *teamharness.Clone, msg string) {
		t.Helper()
		c.Git("add", "-A")
		c.Git("-c", "core.hooksPath=/dev/null", "commit", "-q", "--no-verify", "-m", msg)
	}

	s.Step("D-994/a-note-committed-past-the-hooks-and-removed", func() {
		ada.Lore(pc.Dir, "init", "--non-interactive", "--name=platform").OK(t)
		pc.CommitAll("lore init")
		noteID = oAddedID(t, ada.Lore(pc.Dir, "decision", "add", "--title", "Pricing for the client", "--body", note, "--to", oOrg, "--yes", "--json").OK(t))
		ada.LoreIn(pc, "sync").OK(t)
		pc.WriteFile("PRICING.md", "# Pricing\n\n"+note+"\n")
		pastHooks(pc, "pricing notes")
		pc.Git("rm", "-q", "PRICING.md")
		pastHooks(pc, "drop pricing notes")
		// no own: pattern names this repo, so lore treats it as a client repo (fail
		// closed), as the commit hook does: --history reads its notes and says how
		// an own repo stops being one
		r := ada.Lore(pc.Dir, "check", "leaks", "--history").
			Fails(t, 19, "1 internal note(s) in the history of HEAD", "PRICING.md:3", "internal: matches internal note "+noteID,
				"lore org config set --org "+oOrg+" --key own --add ").
			Never(t, "forty percent below list")
		own := ""
		for _, l := range strings.Split(r.Stdout+r.Stderr, "; ") {
			if _, cmd, ok := strings.Cut(l, "lore org config set "); ok {
				cmd, _, _ = strings.Cut(cmd, "\n")
				own = "org config set " + strings.TrimRight(strings.TrimSpace(cmd), ").")
			}
		}
		if own == "" {
			t.Fatalf("no lore org config set line in the refusal:\n%s", r)
		}
		ada.Lore(pc.Dir, strings.Fields(own)...).OK(t)
		// an own repo may hold its own notes: plain --history reads secrets only and says so
		ada.Lore(pc.Dir, "check", "leaks", "--history").OK(t).
			Says(t, "✓ lore check leaks --history: no secret in", "internal notes not checked: this is an own repo", "lore check leaks --history --as-client")
	})

	s.Step("D-994/history-as-client-names-the-note-with-its-commit", func() {
		r := ada.Lore(pc.Dir, "check", "leaks", "--history", "--as-client").
			Fails(t, 19, "E_LEAK_BLOCKED", "1 internal note(s) in the history of HEAD", "read as the client would",
				"PRICING.md:3", "internal: matches internal note "+noteID, "removed since, still in history",
				"lore leaks allow --note "+noteID+" --file PRICING.md --match-hash ", "the client receives every commit").
			Never(t, "forty percent below list")
		// the printed allow line runs where printed and clears the finding
		allow := ""
		for _, l := range strings.Split(r.Stdout+r.Stderr, "\n") {
			if _, cmd, ok := strings.Cut(l, "approved public text the note quotes? lore "); ok {
				allow = strings.Replace(cmd, `"<why it is public>"`, "approved-public-copy", 1)
			}
		}
		if allow == "" {
			t.Fatalf("no lore leaks allow line under the finding:\n%s", r)
		}
		ada.Lore(pc.Dir, strings.Fields(allow)...).OK(t)
		ada.LoreIn(pc, "sync").OK(t)
		ada.Lore(pc.Dir, "check", "leaks", "--history", "--as-client").OK(t).
			Says(t, "✓ lore check leaks --history: no secret or internal note in", "read as the client would", "1 passage(s) a lore leaks allow entry lets through")
	})

	s.Step("D-994/a-client-repo-history-is-read-for-notes", func() {
		const opsNote = "The client renewal waits until their finance team signs the revised support budget."
		ada.LoreIn(pc, "store", "folder", "add", "team", "client_ops").OK(t)
		ada.Lore(ac.Dir, "org", "link", "--org", oOrg, "--project", "client_app", "--yes").OK(t)
		opsID := oAddedID(t, ada.Lore(ac.Dir, "decision", "add", "--title", "Renewal on hold", "--body", opsNote, "--to", oOrg+"/client_ops", "--yes", "--json").OK(t))
		ada.Lore(ac.Dir, "sync").OK(t)
		ac.WriteFile("NOTES.md", "# Notes\n\n"+opsNote+"\n")
		ac.Git("add", "-A")
		// the commit hook refuses it; a commit past the hooks lands it anyway
		ada.Lore(ac.Dir, "check", "leaks").Fails(t, 19, "NOTES.md:3", opsID)
		pastHooks(ac, "notes")
		ac.Git("rm", "-q", "NOTES.md")
		pastHooks(ac, "drop notes: "+opsNote)
		ada.Lore(ac.Dir, "check", "leaks", "--history").
			Fails(t, 19, "E_LEAK_BLOCKED", "2 internal note(s) in the history of HEAD", "NOTES.md:3", "commit message",
				"internal: matches internal note "+opsID, "lore leaks allow --note "+opsID+" --file NOTES.md").
			Never(t, "finance team signs", "read as the client would")
	})
}
