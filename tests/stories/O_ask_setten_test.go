//go:build stories && !windows

package stories

// O_ask_setten_test.go — devco D-1035 (bug hunt H-296, team-store
// benchmark set 10 asks a and c) on the owner-shaped machine (story O).
// An own repo of the org keeps long as-built notes about its acme invoice
// sweep holding many of a question's common words and none of its key
// word, a memory holding the key word (zorblat) and nearly as many words,
// and a short memory holding most of another question's words. ask
// printed the long notes first, in search's order, and the rows that
// answer as candidate lines. Now a long note without the question's key
// word gives the first place to the row holding it, and a short row
// holding most of the question's words is printed whole before a second
// long note.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOAskSetTen(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	const (
		skip   = "The invoice sweep skips a shop whose zorblat flag is set and logs the skip."
		refund = "On refund the acme invoice sweep drops the retry and closes the shop's draft."
	)
	note := func(title string) string {
		var b strings.Builder
		b.WriteString(title + ". The acme invoice sweep and its retry, as built.\n\n")
		for n := 0; b.Len() <= 3000; n++ {
			fmt.Fprintf(&b, "Stage %d of the acme invoice sweep keeps a cursor per shop, reads the flag set on it and logs each retry and refund to the desk journal.\n", n+1)
		}
		return b.String()
	}
	s.Step("D-1035/long-notes-a-key-word-memory-and-a-short-row", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "platform").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "platform", "--own", "--yes").OK(t)
		ins := []map[string]any{
			{"op": "architecturenote add", "input": map[string]any{"title": "Acme invoice sweep retry design", "body": note("Acme invoice sweep retry design")}},
			{"op": "memory add", "input": map[string]any{"body": skip}},
			{"op": "memory add", "input": map[string]any{"body": refund}},
			{"op": "decision add", "input": map[string]any{"title": "Zorblat refunds", "body": "The acme invoice sweep sends a zorblat refund through the desk with no retry."}},
		}
		for i := range 8 {
			ins = append(ins, map[string]any{"op": "memory add", "input": map[string]any{"body": fmt.Sprintf("Note %d: the acme invoice sweep runs a retry per shop and reads the flag set on it after a refund.", i+1)}})
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

	s.Step("D-1035/the-row-holding-the-key-word-takes-the-first-place", func() {
		q := "does the acme invoice sweep retry a shop whose zorblat flag is set"
		r := ada.Lore(pc.Dir, "ask", "--query", q).OK(t)
		out := r.Out()
		at, long := strings.Index(out, skip), strings.Index(out, "Acme invoice sweep retry design")
		if at < 0 || (long >= 0 && long < at) {
			t.Errorf("[O D-1035] ask %q: the memory holding the key word is not printed before the long note holding none:\n%s", q, r)
		}
	})

	s.Step("D-1035/a-short-row-before-a-second-long-note", func() {
		q := "what does the acme invoice sweep do with a retry on a zorblat refund"
		r := ada.Lore(pc.Dir, "ask", "--query", q).OK(t)
		out := r.Out()
		at, long := strings.Index(out, refund), strings.Index(out, "Acme invoice sweep retry design")
		if at < 0 || (long >= 0 && long < at) {
			t.Errorf("[O D-1035] ask %q: the short row holding most of the question's words is not printed whole before the long note:\n%s", q, r)
		}
	})
}
