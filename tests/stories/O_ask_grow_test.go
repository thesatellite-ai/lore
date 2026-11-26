//go:build stories && !windows

package stories

// O_ask_grow_test.go — devco D-1006 (bug hunt H-268) on the owner-shaped
// machine (story O): what lore ask prints in full is decided by bytes.
// An own repo of the org keeps a 2,400-character as-built note whose
// "Escalation" section holds the answer but no word of the question, next
// to short memories sharing the question's common words. ask cut the note
// to its best 1,000 characters, left the answering sentence out and
// filled the rest of the answer with candidate lines, so the reader
// needed a lore show. The note fits whole next to the first candidate
// lines, so it is printed whole.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOAskGrowByBytes(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	const answer = "After the third failure the dock lead gets a page and the carrier pickup waits."
	s.Step("D-1006/a-long-note-and-memories-sharing-common-words", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "platform").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "platform", "--own", "--yes").OK(t)
		var body strings.Builder
		body.WriteString("# As built: parcel desk\n\n## Label retry policy\n\nA failed label print at the parcel desk is retried by the label worker.\n\n")
		body.WriteString("## Escalation\n\n" + answer + "\n\n")
		filler := []string{
			"Invoices go out on the first weekday; the ledger closes when finance signs the monthly sheet.",
			"Theme colours come from the brand palette; the admin screens use the compact density.",
			"Search synonyms are curated by merchandising and pushed to the index each night.",
		}
		for n := 0; body.Len() < 2400; n++ {
			fmt.Fprintf(&body, "## Notes %d\n\n%s %s\n\n", n+1, filler[n%len(filler)], filler[(n+1)%len(filler)])
		}
		ins := []map[string]any{{"op": "architecturenote add", "input": map[string]any{"title": "As built: parcel desk", "body": body.String()}}}
		for i, w := range []string{"zones", "carriers", "scales", "printers", "pallets", "returns", "rates", "hours", "badges"} {
			ins = append(ins, map[string]any{"op": "memory add", "input": map[string]any{"body": fmt.Sprintf("The parcel desk keeps its %s list in the shared sheet, tab %d.", w, i+1)}})
		}
		var b strings.Builder
		for _, in := range ins {
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

	s.Step("D-1006/the-note-that-fits-is-printed-whole", func() {
		q := "what is the label retry policy of the parcel desk"
		r := ada.Lore(pc.Dir, "ask", "--query", q).OK(t)
		if !strings.Contains(r.Out(), answer) {
			t.Errorf("[O D-1006] ask %q: the note fits whole but its answering sentence is not printed:\n%s", q, r)
		}
		if strings.Contains(r.Out(), "cut to the part that answers") {
			t.Errorf("[O D-1006] ask %q: a row that fits was cut:\n%s", q, r)
		}
		if len(r.Out()) > 4400 {
			t.Errorf("[O D-1006] ask %q: the answer is %d characters, over --max-chars:\n%s", q, len(r.Out()), r)
		}
	})
}
