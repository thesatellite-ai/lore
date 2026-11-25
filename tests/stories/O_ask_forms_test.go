//go:build stories && !windows

package stories

// O_ask_forms_test.go — devco D-976 (bug hunt H-235, Sync benchmark set 6
// item 5: S6-05, S6-28, S6-34 and the sibling questions S6-27/28/29) on
// the owner-shaped machine (story O): an own repo of the org linked to
// its folder keeps a memory on catalog validation rules and one on the
// enqueue gate, next to a rule and an as-built note about something else
// that write the questions' other words in the questions' own form. A
// question writing a word in another form than the row (validate for
// validation, enqueuing for enqueue, jobs for job) did not match in lore
// ask, so sibling questions on one subject got different first rows and
// the off-topic row stood first, while lore search (stemmed) listed the
// answering row first. Now each sibling gets the same first row, the one
// search lists first.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOAskWordForms(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	home := oOrg + "/catalogapp"

	s.Step("D-976/rows-in-other-word-forms", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "catalogapp").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "catalogapp", "--own", "--yes").OK(t)
		var b strings.Builder
		line := func(op string, in map[string]any) {
			raw, err := json.Marshal(map[string]any{"op": op, "input": in})
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
			b.WriteByte('\n')
		}
		line("rule add", map[string]any{"body": "One writer per catalog table: only the catalog worker writes product fields and rules; other services read them."})
		line("architecturenote add", map[string]any{"title": "Attribute value writer", "body": "The attribute value writer runs sync jobs per product; a gate skips the jobs of a product whose values did not change.", "meta": []string{"fact_state=live", "fact_verified_at=2026-11-20"}})
		for _, c := range []string{"The catalog worker logs each product it reads.", "A sync job prints the product it wrote.", "Each product field is stored with its value.", "The worker reads each catalog table.", "The job queue keeps each job for a week.", "A product has a title, a price and its fields.", "The catalog sync runs every hour."} {
			line("memory add", map[string]any{"body": c})
		}
		for i := range 12 {
			line("memory add", map[string]any{"body": fmt.Sprintf("Note %d: the dashboard lists each workspace with its plan, owner and the last deploy.", i)})
		}
		line("memory add", map[string]any{"body": "Catalog validation: each validation rule holds a field, an operator and a severity; an error severity blocks the publish, a warning only shows."})
		line("memory add", map[string]any{"body": "The enqueue gate refuses a job while the same job key is still queued and returns the id of the first job."})
		batch := filepath.Join(t.TempDir(), "rows.jsonl")
		if err := os.WriteFile(batch, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		ada.Lore(pc.Dir, "batch", "apply", "--file", batch, "--yes").OK(t)
		ada.Lore(pc.Dir, "whoami").OK(t).Says(t, home)
	})

	s.Step("D-976/siblings-get-search's-first-row-first", func() {
		for _, c := range []struct{ q, want string }{
			{"what does a catalog validation rule hold", "Catalog validation"},
			{"which product fields and rules does the catalog validate", "Catalog validation"},
			{"how are catalog validations of a field run", "Catalog validation"},
			{"what does the gate check before enqueuing jobs", "The enqueue gate"},
		} {
			var found struct {
				Data struct {
					Hits []struct {
						ID string `json:"id"`
					} `json:"hits"`
				} `json:"data"`
			}
			r := ada.Lore(pc.Dir, "search", "--query", c.q, "--json").OK(t)
			if err := json.Unmarshal([]byte(r.Out()), &found); err != nil || len(found.Data.Hits) == 0 {
				t.Fatalf("[O D-976] search --json %q: %v\n%s", c.q, err, r)
			}
			var asked struct {
				Rows []struct {
					ID   string `json:"id"`
					Body string `json:"body"`
				} `json:"rows"`
			}
			r = ada.Lore(pc.Dir, "ask", "--query", c.q, "--json").OK(t)
			if err := json.Unmarshal([]byte(r.Out()), &asked); err != nil {
				t.Fatalf("[O D-976] ask --json %q: %v\n%s", c.q, err, r)
			}
			if len(asked.Rows) == 0 || !strings.HasPrefix(asked.Rows[0].Body, c.want) || asked.Rows[0].ID != found.Data.Hits[0].ID {
				t.Errorf("[O D-976] %q: ask's first row is not the one starting %q that search lists first (%s):\n%s", c.q, c.want, found.Data.Hits[0].ID, ada.Lore(pc.Dir, "ask", "--query", c.q).OK(t))
			}
		}
	})
}
