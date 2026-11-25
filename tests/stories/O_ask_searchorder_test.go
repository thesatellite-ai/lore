//go:build stories && !windows

package stories

// O_ask_searchorder_test.go — devco D-984 (bug hunt H-243, owner decision
// "lore ask follows search") on the owner-shaped machine (story O): an own
// repo of the org linked to its folder keeps as-built notes of two
// subsystems that share the folder's common words, and short rows naming
// the asked command. lore ask ranked by a scorer of its own (folder,
// verified date, sections, identifier bonuses …) and read the older
// as-built note as replaced by the newer one, printing it as history.
// Now ask's rows follow search's order: search's first row is ask's first
// row, and a note of another subsystem is never history.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOAskFollowsSearch(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	s.Step("D-984/two-subsystems-and-short-rows", func() {
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
		common := "Every worker command prints the run it started; a failed job is retried with backoff and the config names every default."
		for _, n := range []string{"As built: hookrelay (webhook sender)", "As built: ratewall (rate limiter)"} {
			var body strings.Builder
			body.WriteString("# " + n + "\n\n")
			for i := 1; i <= 12; i++ {
				fmt.Fprintf(&body, "## Part %d\n\n%s\n\n", i, common)
			}
			line("architecturenote add", map[string]any{"title": n, "body": body.String()})
		}
		line("memory add", map[string]any{"body": "hookrelay replay --since <day> resends every webhook that failed since that day."})
		line("memory add", map[string]any{"body": "ratewall keeps one bucket per tenant key."})
		batch := filepath.Join(t.TempDir(), "rows.jsonl")
		if err := os.WriteFile(batch, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		ada.Lore(pc.Dir, "batch", "apply", "--file", batch, "--yes").OK(t)
	})

	s.Step("D-984/ask-first-row-is-search-first-row", func() {
		for _, q := range []string{"how does hookrelay resend a failed webhook", "how does a worker command retry a failed job"} {
			var found struct {
				Data struct {
					Hits []struct {
						ID string `json:"id"`
					} `json:"hits"`
				} `json:"data"`
			}
			r := ada.Lore(pc.Dir, "search", "--query", q, "--json").OK(t)
			if err := json.Unmarshal([]byte(r.Out()), &found); err != nil || len(found.Data.Hits) == 0 {
				t.Fatalf("[O D-984] search --json %q: %v\n%s", q, err, r)
			}
			var asked struct {
				Rows []struct {
					ID      string `json:"id"`
					Title   string `json:"title"`
					History string `json:"history"`
				} `json:"rows"`
			}
			r = ada.Lore(pc.Dir, "ask", "--query", q, "--json").OK(t)
			if err := json.Unmarshal([]byte(r.Out()), &asked); err != nil || len(asked.Rows) == 0 {
				t.Fatalf("[O D-984] ask --json %q: %v\n%s", q, err, r)
			}
			if asked.Rows[0].ID != found.Data.Hits[0].ID {
				t.Errorf("[O D-984] %q: ask's first row %s is not search's first row %s:\n%s", q, asked.Rows[0].ID, found.Data.Hits[0].ID, ada.Lore(pc.Dir, "ask", "--query", q).OK(t))
			}
			for _, row := range asked.Rows {
				if strings.HasPrefix(row.Title, "As built:") && row.History != "" {
					t.Errorf("[O D-984] %q: the note %q of another subsystem is printed as history (%s)", q, row.Title, row.History)
				}
			}
		}
	})
}
