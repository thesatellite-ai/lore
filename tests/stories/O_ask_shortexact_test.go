//go:build stories && !windows

package stories

// O_ask_shortexact_test.go — devco D-972 (bug hunt H-231, Sync benchmark
// set 6 item 1: S6-06, 09, 10, 18, 24, 32, 33, 34) on the owner-shaped
// machine (story O): an own repo of the org linked to its folder keeps
// several-KB as-built notes that each hold the folder's common words
// (tool, cli, commands, workflow, run) in sections of their own, and the
// short memory naming the asked command (wkf trigger / reconcile /
// schedule). lore search lists the memory first; lore ask ranked three
// notes above it (every word of a note's whole text counted, plus the
// common words of its best section's heading). Now search's first row is
// in ask's top three.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestStoryOAskShortExactRow(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	home := oOrg + "/syncapp"

	s.Step("D-972/long-notes-and-a-short-exact-row", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "syncapp").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "syncapp", "--own", "--yes").OK(t)
		var b strings.Builder
		line := func(op string, in map[string]any) {
			raw, err := json.Marshal(map[string]any{"op": op, "input": in})
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
			b.WriteByte('\n')
		}
		filler := "The parcel desk prints zebra labels at the dock and carriers collect parcels twice daily. Theme colours come from the brand palette."
		for _, n := range [][3]string{
			{"As built: cron binary", "Schedules|The cron binary reads each schedule from the store and starts a workflow run when it is due.", "Callbacks|A finished workflow run posts its result to the callback of the workflow."},
			{"As built: workflow engine", "Run lifecycle|A workflow run moves from queued to running to done; the engine schedules each step.", "Timers|A timer step waits until its schedule fires, then the run goes on."},
			{"As built: job queue", "Enqueue|A job is enqueued with a kind and a payload.", "Retries|A failed job is retried with backoff up to five times."},
			{"As built: file manager", "Upload path|A file upload goes to the upload handler.", "Mirror uploads|After an upload a worker job copies the file to the mirror bucket."},
			{"As built: auth manager", "Stored credentials|Partner credentials are stored encrypted.", "Signing keys|Tokens are signed with a key from the key ring."},
		} {
			var body strings.Builder
			body.WriteString("# " + n[0] + "\n\n")
			for _, sec := range n[1:] {
				h, text, _ := strings.Cut(sec, "|")
				body.WriteString("## " + h + "\n\n" + text + "\n\n")
			}
			body.WriteString("## Tool cli commands\n\nThe tool cli has a command per workflow; each command prints the run it started and the worker that took it.\n\n")
			body.WriteString("## Stored keys and tables\n\nEach run is stored in a table with its key; a failed run keeps its row so the worker can read it on the next run.\n\n")
			for i := 1; i <= 14; i++ {
				fmt.Fprintf(&body, "## Notes %d\n\n%s\n\n", i, filler)
			}
			line("architecturenote add", map[string]any{"title": n[0], "body": body.String(), "meta": []string{"fact_state=live", "fact_verified_at=2026-11-20"}})
		}
		for _, c := range []string{"A tool cli command prints the workflow run it started.", "Each workflow run is stored in the run table with its key.", "The worker restarts when a command fails; the run is kept.", "The tool worker reads each workflow from the table."} {
			line("memory add", map[string]any{"body": c})
		}
		for i := range 16 {
			line("memory add", map[string]any{"body": fmt.Sprintf("Note %d: the dashboard lists each workspace with its plan, owner and the last deploy.", i)})
		}
		line("memory add", map[string]any{"body": "tool cli wkf trigger / reconcile / schedule: trigger starts one workflow run now, reconcile re-checks stuck runs, schedule prints the next due run."})
		batch := filepath.Join(t.TempDir(), "rows.jsonl")
		if err := os.WriteFile(batch, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		ada.Lore(pc.Dir, "batch", "apply", "--file", batch, "--yes").OK(t)
		ada.Lore(pc.Dir, "whoami").OK(t).Says(t, home)
	})

	s.Step("D-972/search-first-row-in-ask-top-three", func() {
		q := "which tool cli commands trigger, reconcile or schedule a workflow run"
		var found struct {
			Data struct {
				Hits []struct {
					ID   string `json:"id"`
					Body string `json:"snippet"`
				} `json:"hits"`
			} `json:"data"`
		}
		r := ada.Lore(pc.Dir, "search", "--query", q, "--json").OK(t)
		if err := json.Unmarshal([]byte(r.Out()), &found); err != nil || len(found.Data.Hits) == 0 {
			t.Fatalf("[O D-972] search --json: %v\n%s", err, r)
		}
		var asked struct {
			Rows []struct {
				ID   string `json:"id"`
				Body string `json:"body"`
			} `json:"rows"`
		}
		r = ada.Lore(pc.Dir, "ask", "--query", q, "--json").OK(t)
		if err := json.Unmarshal([]byte(r.Out()), &asked); err != nil {
			t.Fatalf("[O D-972] ask --json: %v\n%s", err, r)
		}
		var ids []string
		answered := false
		for _, row := range asked.Rows[:min(3, len(asked.Rows))] {
			ids = append(ids, row.ID)
			answered = answered || strings.Contains(row.Body, "wkf trigger")
		}
		if !answered || !slices.Contains(ids, found.Data.Hits[0].ID) {
			t.Errorf("[O D-972] search's first row %s (the memory naming wkf trigger) is not in ask's top three %v:\n%s", found.Data.Hits[0].ID, ids, ada.Lore(pc.Dir, "ask", "--query", q).OK(t))
		}
	})
}
