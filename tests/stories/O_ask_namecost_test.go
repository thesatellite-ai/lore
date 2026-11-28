//go:build stories && !windows

package stories

// O_ask_namecost_test.go — devco D-1036 (bug hunt H-300) on the
// owner-shaped machine (story O): one question of a benchmark set took
// 5 s where every other took under 1.5 s. It was long (about 30 words)
// and named three resources that have aliases, and the notes it scored
// held many identifiers: for every row, ask checked every question word
// against every identifier and rebuilt each resource's names through a
// regexp on each check. Now a resource's names are built once per ask
// and the answer is kept per word, so the long question costs about what
// a short one does.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoryOAskLongQuestionManyIdentifiers(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	s.Step("D-1036/three-resources-with-aliases-and-notes-full-of-identifiers", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "platform").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "platform", "--own", "--yes").OK(t)
		ada.Lore(pc.Dir, "resource", "kind", "add", "--kind", "code_repo", "--yes").OK(t)
		ada.Lore(pc.Dir, "resource", "kind", "add", "--kind", "person", "--name-format", "dns", "--yes").OK(t)
		for _, r := range []struct{ kind, name, a1, a2 string }{
			{"code_repo", "widget_api", "Widget Gateway", "the widget service"},
			{"person", "bob-lee", "Bobby", "Robert Lee"},
			{"code_repo", "acme_portal", "Acme Portal", "the customer portal"},
		} {
			ada.Lore(pc.Dir, "resource", "add", "--kind", r.kind, "--name", r.name, "--yes").OK(t)
			ada.Lore(pc.Dir, "resource", "alias", "add", "--kind", r.kind, "--id", r.name, "--alias", r.a1, "--alias", r.a2).OK(t)
		}
		parts := []string{"signing", "keys", "rotates", "token", "install", "link", "redirect", "hmac", "carry", "send", "none", "back"}
		var b strings.Builder
		for i := range 300 {
			var body strings.Builder
			fmt.Fprintf(&body, "# widget_api signing notes %d\n\nacme_portal rotates keys; bob-lee reviews.\n\n", i)
			for n := range 80 {
				a, c := parts[n%len(parts)], parts[(n*5+3)%len(parts)]
				fmt.Fprintf(&body, "- `%s%s%d` and `%s_%s_v%d` hold the %s step.\n", a, strings.ToUpper(c[:1])+c[1:], n, c, a, n, a)
			}
			raw, err := json.Marshal(map[string]any{"op": "memory add", "input": map[string]any{"body": body.String()}})
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
		ada.Lore(pc.Dir, "ask", "--query", "widget_api signing").OK(t) // the first call builds the index
	})

	s.Step("D-1036/a-long-question-costs-about-what-a-short-one-does", func() {
		timed := func(q string) time.Duration {
			start := time.Now()
			ada.Lore(pc.Dir, "ask", "--query", q).OK(t).Says(t, "widget_api signing notes")
			return time.Since(start)
		}
		short := "how does widget_api sign the install link?"
		long := "When acme_portal rotates two signing keys on widget_api and bob-lee has none at all, which token does the install link carry and which hmac does the redirect send back?"
		// best of two each, so one slow moment of a busy machine does not decide
		ts, tl := min(timed(short), timed(short)), min(timed(long), timed(long))
		t.Logf("short question %v, long question %v", ts, tl)
		// before the fix: about 4x the short question (0.9 s and 3.7 s on
		// a developer machine); now about 1.3x
		if tl > 2*ts+500*time.Millisecond {
			t.Errorf("[O D-1036] the long question naming three resources took %v, the short one %v: ask's work grows with question words x identifiers x resources", tl, ts)
		}
	})
}
