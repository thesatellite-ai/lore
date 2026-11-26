//go:build stories && !windows

package stories

// O_ask_yield_test.go — devco D-1011 (bug hunt H-273) on the owner-shaped
// machine (story O): the first row's room comes before a later row's cut.
// An own repo of the org keeps two long as-built notes on the question's
// subject next to short memories sharing its common words. ask cut both
// notes to about 1,000 characters next to five candidate lines, so the
// first note was printed first but cut short of room it could have had;
// now the second note's cut is a candidate line and the first note is
// printed whole or widened to the freed room.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOAskFirstRowRoom(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	const answer = "After three tries the job moves to the dead-letter tray and the dock lead gets a page."
	s.Step("D-1011/two-long-notes-on-the-subject-and-memories", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "platform").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "platform", "--own", "--yes").OK(t)
		pad := " The night shift checks the dock board at every change of shift, and the board lists each station with its owner, its spare parts shelf and the phone number of the facilities desk that answers outside office hours."
		filler := []string{
			"Invoices go out on the first weekday; the ledger closes when finance signs the monthly sheet.",
			"Theme colours come from the brand palette; the admin screens use the compact density.",
			"Search synonyms are curated by merchandising and pushed to the index each night.",
		}
		note := func(title string, secs ...[2]string) string {
			var b strings.Builder
			b.WriteString("# " + title + "\n\n")
			for _, sec := range secs {
				b.WriteString("## " + sec[0] + "\n\n" + sec[1] + "\n\n")
			}
			for n := 0; b.Len() < 3600; n++ {
				fmt.Fprintf(&b, "## Notes %d\n\n%s %s\n\n", n+1, filler[n%len(filler)], filler[(n+1)%len(filler)])
			}
			return b.String()
		}
		ins := []map[string]any{
			{"op": "architecturenote add", "input": map[string]any{"title": "As built: label worker", "body": note("As built: label worker",
				[2]string{"Label worker queue", "The label worker reads print jobs from the label queue; each job names its printer, its template and its parcel." + pad},
				[2]string{"Label worker printers", "Every printer of the label worker has a heartbeat; a printer missing two heartbeats is skipped by the label queue." + pad},
				[2]string{"Label worker templates", "Label templates are versioned; the label worker renders a job with the template version the queue names." + pad},
				[2]string{"Label worker retries", answer})}},
			{"op": "architecturenote add", "input": map[string]any{"title": "As built: label printers", "body": note("As built: label printers",
				[2]string{"Label printers", "The label printers sit at the dock; the label worker sends them jobs over the print network." + pad},
				[2]string{"Firmware", "Printer firmware is updated by the facilities team on the first weekday of the month." + pad})}},
		}
		for i, w := range []string{"zones", "carriers", "scales", "pallets", "returns", "rates", "hours", "badges", "bins", "tapes"} {
			ins = append(ins, map[string]any{"op": "memory add", "input": map[string]any{"body": fmt.Sprintf("The dock desk keeps its %s list in the shared sheet, tab %d, next to the open parcels, the dock worker rota and the carrier pickup times for the week.", w, i+1)}})
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

	s.Step("D-1011/the-first-note-gets-the-room-of-the-second-cut", func() {
		q := "how does the label worker handle a failed label"
		// the notes are sized to a 4,000-character answer, where neither
		// fits whole (the default is 8,000 since devco D-1013)
		r := ada.Lore(pc.Dir, "ask", "--query", q, "--max-chars", "4000").OK(t)
		out := r.Out()
		if !strings.Contains(out, answer) {
			t.Errorf("[O D-1011] ask %q: the first note's answering sentence is not printed:\n%s", q, r)
		}
		if n := strings.Count(out, "cut to the part that answers"); n > 1 {
			t.Errorf("[O D-1011] ask %q: %d rows printed cut; a later row's cut should be a candidate line:\n%s", q, n, r)
		}
		if !strings.Contains(out, "As built: label printers") {
			t.Errorf("[O D-1011] ask %q: the second note is neither printed nor a candidate line:\n%s", q, r)
		}
		if len(out) > 4400 {
			t.Errorf("[O D-1011] ask %q: the answer is %d characters, over --max-chars:\n%s", q, len(out), r)
		}
	})
}
