//go:build stories && !windows

package stories

// O_ask_cover_test.go — devco D-985 (bug hunt H-244, owner decision
// 2026-11-25 item 2: lore ask follows search) on the owner-shaped
// machine (story O): an own repo of the org linked to its folder keeps a
// long as-built note whose short sections hold the question's common
// words and whose long "Limits" section holds the one sentence on the
// asked window. ask cut the note to its best sections, the Limits
// section did not fit, and the cut note said the part left out holds
// window: an agent reading only the page answered "not in memory". Now
// the cut adds the sentence holding the word under its section's path.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOAskCoverLeftOut(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	const answer = "The window opens at nine and closes at ten."
	s.Step("D-985/long-note-with-the-answer-in-a-long-section", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "platform").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "platform", "--own", "--yes").OK(t)
		var body strings.Builder
		body.WriteString("# As built: orbit\n\n## Rollout plan\n\nAn orbit rollout goes to the canary region first, then region by region.\n\n")
		body.WriteString("## Overview\n\nOrbit is the release tool of the platform; a rollout is one release to every region.\n\n")
		body.WriteString("## Limits\n\n")
		for range 30 {
			body.WriteString("The team keeps notes on every past release and reviews them each quarter. ")
		}
		body.WriteString(answer + "\n\n## Contacts\n\nAsk the release desk for anything else.\n")
		var b strings.Builder
		for _, in := range []map[string]any{
			{"op": "architecturenote add", "input": map[string]any{"title": "As built: orbit", "body": body.String()}},
			{"op": "memory add", "input": map[string]any{"body": "The release desk answers questions about tools on weekdays."}},
		} {
			raw, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
			b.WriteByte('\n')
		}
		batch := filepath.Join(t.TempDir(), "rows.jsonl")
		if err := os.WriteFile(batch, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		ada.Lore(pc.Dir, "batch", "apply", "--file", batch, "--yes").OK(t)
	})

	s.Step("D-985/the-cut-holds-every-asked-word-the-row-holds", func() {
		q := "when does the orbit rollout of the canary region open its window"
		r := ada.Lore(pc.Dir, "ask", "--query", q, "--max-chars", "1200").OK(t)
		if !strings.Contains(r.Out(), answer) {
			t.Errorf("[O D-985] ask %q: the cut note left out the sentence on the change window:\n%s", q, r)
		}
		if strings.Contains(r.Out(), "left out holds") {
			t.Errorf("[O D-985] ask %q: the cut note still names an asked word as left out:\n%s", q, r)
		}
	})
}
