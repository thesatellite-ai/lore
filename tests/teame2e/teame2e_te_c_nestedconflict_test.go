//go:build teame2e

package teame2e

// te-c nested conflicts (later 461, OV14 tests; METADATA 3.12, META-B8-GIT):
// a conflict that sits INSIDE a row — a strict metadata key two teammates
// set differently, a marker block in a metadata text value — blocks the two
// ways a row leaves a person's hands: landing from the live ref onto main,
// and a git commit of a row file. The top-level twins are
// TestF04_TextClashKeepsBothAndBlocksLanding and
// TestB11Sync_PreCommitStagesLoreDataAndRefusesBrokenFiles.

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teCMetaKeyID adds a strict number key in c's repo and returns its id.
func teCMetaKeyID(t *testing.T, c *th.Clone, name string) string {
	t.Helper()
	r := c.MustLore("meta", "add", name, "--type", "number", "--strict", "--json")
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil || !strings.HasPrefix(v.ID, "mky_") {
		t.Fatalf("meta add --json: %v %s", err, r)
	}
	return v.ID
}

// TestTeC_NestedMetaConflictBlocksLanding: dev1 and dev2 set the strict key
// cost on the same live task to 4 and 5 without pushing, then both sync.
// The live row keeps both values in a structured conflict entry, `sync
// conflicts` lists the task, `lore land` refuses it and main never gets the
// row; after dev1 settles the key the task lands.
func TestTeC_NestedMetaConflictBlocksLanding(t *testing.T) {
	rt := newRepoTeam(t, capLand, th.Cmd("meta", "add"))
	key := teCMetaKeyID(t, rt.c1, "cost")
	rt.c1.CommitAll("meta key")
	rt.c1.AdminPush("origin", "main")
	rt.c2.Git("pull", "-q", "--ff-only", "origin", "main")

	id := rt.addTask(t, rt.c1, "estimate the migration")
	rt.c2.MustLore("task", "list")
	rt.c1.With(autopushOff).MustLore("task", "edit", id, "--meta", "cost=4")
	rt.c2.With(autopushOff).MustLore("task", "edit", id, "--meta", "cost=5")
	rt.c1.MustLore("sync")
	rt.c2.MustLore("sync")

	row := mustRow(t, rt.app, refLive, liveRowPath(rootFolder, tblTasks, id))
	meta := string(row.Raw["meta"])
	if !strings.Contains(meta, key) || !strings.Contains(meta, `"conflict"`) || !strings.Contains(meta, "4") || !strings.Contains(meta, "5") {
		t.Fatalf("a strict key both sides changed must keep both values in a conflict entry: meta=%s", meta)
	}
	rt.c2.MustLore("sync", "conflicts").Says(t, id)
	rt.c2.Lore("land", id).Refused(t, "conflict")
	if _, ok := rt.mainTask(t, id); ok {
		t.Fatal("a row with a nested metadata conflict must never land")
	}

	// Settling the key with the command the merge report names (METADATA
	// 3.12: lore meta set <id> <key>=<value>) clears the conflict; the row
	// lands like any finished row.
	rt.c2.MustLore("meta", "set", id, "cost=5")
	rt.c2.MustLore("task", "done", id)
	if _, ok := rt.mainTask(t, id); !ok {
		t.Fatalf("after the key is settled the task must land: %s", rt.c2.MustLore("sync", "conflicts"))
	}
}

// TestTeC_NestedConflictBlocksCommit: a row file whose conflict sits inside
// the document — a structured meta conflict entry, or a marker block in a
// metadata text value or a list item — is refused by the pre-commit hook
// with the reason; the same file without the nesting commits.
func TestTeC_NestedConflictBlocksCommit(t *testing.T) {
	c := newSyncSolo(t).c
	key := teCMetaKeyID(t, c, "cost")
	id := mustID(t, c.MustLore("rule", "add", "--body", "use JWT for auth", "--no-live"), "rul")
	c.CommitAll("rule")
	clean, ok := c.ReadFile(ruleFile(id))
	if !ok {
		t.Fatal("precondition: the rule's row file is on the branch")
	}
	block := strconv.Quote("<<<<<<< 0001\nmine\n=======\ntheirs\n>>>>>>> 0002")
	cases := map[string]struct {
		meta string
		why  string
	}{
		"structured meta conflict":          {`{"` + key + `":{"name":"cost","strict":true,"conflict":{"base":3,"ours":4,"theirs":5}}}`, "unsettled metadata conflict"},
		"marker block in a meta text value": {`{"` + key + `":{"name":"cost","v":` + block + `}}`, "conflict markers"},
		"marker block in a meta list item":  {`{"` + key + `":{"name":"cost","v":["a",` + block + `]}}`, "conflict markers"},
	}
	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			c.WriteFile(ruleFile(id), teCWithMeta(t, clean, cs.meta))
			c.WriteFile("src/x.go", "package src // "+name+"\n")
			c.Git("add", "src/x.go")
			r := c.TryGit("commit", "-q", "-m", "code with a "+name)
			if r.Code == 0 || !strings.Contains(r.Out(), "refusing to commit broken lore files") || !strings.Contains(r.Out(), cs.why) {
				t.Fatalf("a row with a %s must refuse the commit with %q: %s", name, cs.why, r)
			}
			c.Git("checkout", "--", ruleFile(id))
			c.Git("reset", "-q", "src/x.go")
		})
	}
	// The same document with a settled value commits.
	c.WriteFile(ruleFile(id), teCWithMeta(t, clean, `{"`+key+`":{"name":"cost","v":5}}`))
	c.WriteFile("src/x.go", "package src // settled\n")
	c.Git("add", "src/x.go")
	if r := c.TryGit("commit", "-q", "-m", "settled"); r.Code != 0 {
		t.Fatalf("a row with a settled meta value must commit: %s", r)
	}
}

// teCWithMeta returns the row document doc with its "meta" set to meta.
func teCWithMeta(t *testing.T, doc, meta string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("row file: %v\n%s", err, doc)
	}
	m["meta"] = json.RawMessage(meta)
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // keep "<<<<<<<" as written, like a real merge leaves it
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
