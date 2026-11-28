//go:build stories && !windows

package stories

// O_ask_named_test.go — devco D-1037 (bug hunt H-297, team-store
// benchmark set 10 ask b) on the owner-shaped machine (story O). An own
// repo of the org keeps a note on its acme app registry that holds every
// word of a question and names the decision with the rule by id
// (${row:<id>}), twenty notes holding most of the question's words, and a
// decision named by a bare id that holds none of them. ask printed the
// note and the twenty fillers and left the decision it names out (or a
// candidate line far down), so the reader saw an id it could not read.
// Now the named decision holding a word of the question is printed right
// after the note naming it, and the one holding none is candidate 1.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOAskNamedRow(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	const (
		q     = "when may an acme app rejoin its workspace after it deleted its own data"
		rule  = "An app comes back only after it removed itself, capped by its previous grant."
		quiet = "Comes back with the grant it held before, never a wider one."
	)
	var dec, other string
	s.Step("D-1037/a-note-naming-decisions-by-id", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "platform").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "platform", "--own", "--yes").OK(t)
		dec = aID(t, ada.Lore(pc.Dir, "decision", "add", "--title", "Comeback cap", "--body", rule, "--yes").OK(t), "dec")
		other = aID(t, ada.Lore(pc.Dir, "decision", "add", "--title", "Grant ceiling", "--body", quiet, "--yes").OK(t), "dec")
		ins := []map[string]any{{"op": "memory add", "input": map[string]any{
			"body": "The acme app registry keeps one install per workspace; an app that deleted its own data may rejoin. Rules for rejoining: ${row:" + dec + "}; the grant ceiling is " + other + "."}}}
		for i := range 20 {
			ins = append(ins, map[string]any{"op": "memory add", "input": map[string]any{"body": fmt.Sprintf("Note %d: the acme app keeps its workspace data and may rejoin it later.", i+1)}})
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

	s.Step("D-1037/the-named-decision-is-printed-or-candidate-one", func() {
		r := ada.Lore(pc.Dir, "ask", "--query", q).OK(t)
		out := r.Out()
		note := strings.Index(out, "Rules for rejoining")
		if note < 0 {
			t.Fatalf("[O D-1037] ask %q: the note naming the decisions is not printed (the step needs it):\n%s", q, r)
		}
		if at := strings.Index(out, rule); at < note {
			t.Errorf("[O D-1037] ask %q: the decision the printed note names, holding a word of the question, is not printed after it:\n%s", q, r)
		}
		cands := out[max(strings.Index(out, "next candidates"), 0):]
		first := ""
		if lines := strings.Split(cands, "\n"); len(lines) > 1 {
			first = lines[1]
		}
		if !strings.Contains(out, quiet) && !strings.HasPrefix(strings.TrimSpace(first), other) {
			t.Errorf("[O D-1037] ask %q: the decision the note names by a bare id, holding none of the question's words, is neither printed nor candidate 1:\n%s", q, r)
		}
		// the printed next step runs where printed and prints the row whole
		ada.Lore(pc.Dir, "show", "--id", other).OK(t).Says(t, quiet)
	})
}
