//go:build stories && !windows

package stories

// O_ask_notinmemory_test.go — devco D-974 (bug hunt H-233, Sync benchmark
// set 6 S6-01) on the owner-shaped machine (story O): an own repo of the
// org linked to its folder keeps a long as-built job-queue note whose
// duplicate-enqueue sentence sits in a section deep in it, and short
// notes sharing the question's common words. lore ask printed "not in
// memory: no row holds queue.enqueue, enqueueoptions.dedupkey" above the
// note that answered, because the hint read a code identifier the
// question writes as one word no row spells. Now a question writing
// identifiers, or a second sentence starting with a capital, gets no hint
// while a returned row holds the asked terms; a cut that leaves the
// answer out names the question's words it left out and lore show --id;
// a question naming an identifier no row holds still gets the hint.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestStoryOAskNotInMemoryHeld(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	home := oOrg + "/jobsapp"

	s.Step("D-974/long-note-answer-deep", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "jobsapp").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "jobsapp", "--own", "--yes").OK(t)
		var b strings.Builder
		line := func(op string, in map[string]any) {
			raw, err := json.Marshal(map[string]any{"op": op, "input": in})
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
			b.WriteByte('\n')
		}
		var arch strings.Builder
		for i, head := range []string{"Overview", "Workers", "Retries", "Run states"} {
			fmt.Fprintf(&arch, "## %s\n\n", head)
			for p := range 3 {
				fmt.Fprintf(&arch, "Section %d part %d: the job queue keeps each run of a job in the queue table; a worker claims the run, heartbeats while it runs and records whether the run returned an error. ", i, p)
			}
			arch.WriteString("\n\n")
		}
		arch.WriteString("## Deduplication\n\nEnqueue takes options; when the dedup key of a new job matches a run that is not terminal yet, Enqueue writes nothing and returns the first run's id.\n")
		line("architecturenote add", map[string]any{"title": "As built: runq, the background job queue", "body": arch.String()})
		for i := range 8 {
			line("memory add", map[string]any{"body": fmt.Sprintf("Note %d: the job queue page lists each run of a job, its worker and the error it returned.", i)})
		}
		batch := filepath.Join(t.TempDir(), "rows.jsonl")
		if err := os.WriteFile(batch, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		ada.Lore(pc.Dir, "batch", "apply", "--file", batch, "--yes").OK(t)
		ada.Lore(pc.Dir, "whoami").OK(t).Says(t, home)
	})

	s.Step("D-974/no-false-not-in-memory", func() {
		for _, q := range []string{
			"What does Queue.Enqueue return when EnqueueOptions.DedupKey matches a run that is not terminal?",
			"what does the job queue do with a dedup key while the first run is not terminal? Explain the run states.",
		} {
			r := ada.Lore(pc.Dir, "ask", "--query", q).OK(t)
			if strings.Contains(r.Out(), "not in memory") || !strings.Contains(r.Out(), "runq") {
				t.Errorf("[O D-974] %q: a false not-in-memory hint, or the runq note missing:\n%s", q, r)
			}
		}
		r := ada.Lore(pc.Dir, "ask", "--query", "what does Queue.Reprioritize do with a waiting run").OK(t)
		if !strings.HasPrefix(r.Out(), "not in memory: no row holds queue.reprioritize;") {
			t.Errorf("[O D-974] an identifier no row holds gives no hint:\n%s", r)
		}
	})

	s.Step("D-974/cut-names-the-words-left-out", func() {
		// a small --max-chars cuts the note to its opening sections: the
		// cut note names what the part left out holds and where to read it
		q := "how does the job queue handle a dedup key while the first run is not terminal"
		r := ada.Lore(pc.Dir, "ask", "--query", q, "--max-chars", "400", "--json").OK(t)
		var out struct {
			Rows []struct {
				ID      string   `json:"id"`
				Title   string   `json:"title"`
				Body    string   `json:"body"`
				LeftOut []string `json:"left_out"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(r.Out()), &out); err != nil {
			t.Fatalf("[O D-974] ask --json: %v\n%s", err, r)
		}
		seen := false
		for _, row := range out.Rows {
			if !strings.Contains(row.Title, "runq") {
				continue
			}
			seen = true
			if !strings.Contains(row.Body, "first run's id") && !slices.Contains(row.LeftOut, "dedup") {
				t.Errorf("[O D-974] the runq note was cut without its answer and its cut note does not name dedup (left_out %v):\n%s", row.LeftOut, r)
			}
			if len(row.LeftOut) == 0 {
				continue
			}
			text := ada.Lore(pc.Dir, "ask", "--query", q, "--max-chars", "400").OK(t)
			if !strings.Contains(text.Out(), "the part left out holds "+strings.Join(row.LeftOut, ", ")+"; lore show --id "+row.ID+" prints it whole") {
				t.Errorf("[O D-974] the cut note does not name the words left out:\n%s", text)
			}
		}
		if !seen {
			t.Errorf("[O D-974] the runq note is not in the answer:\n%s", r)
		}
	})
}
