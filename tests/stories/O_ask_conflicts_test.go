//go:build stories && !windows

package stories

// O_ask_conflicts_test.go — devco D-980 (bug hunt H-239, Sync benchmark
// set 7 item 5) on the owner-shaped machine (story O): an own repo of the
// org linked to its folder keeps a long as-built note (fact_state live,
// verified) saying a client's request_timeout defaults to 5000 ms and a
// memory saying it has no default. lore ask printed both rows, one under
// the other, with no word that they disagree, and an agent took the first.
// Now the answer prints one line naming the field, both values and both
// rows (the live, verified one first) with the lore show commands, and
// --json a conflicts entry; rows agreeing in other units (5 s, 5000 ms)
// and rows only naming a field print none for it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOAskConflictHint(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	s.Step("D-980/rows-on-one-field", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "gatewayapp").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "gatewayapp", "--own", "--yes").OK(t)
		var b strings.Builder
		line := func(op string, in map[string]any) {
			raw, err := json.Marshal(map[string]any{"op": op, "input": in})
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
			b.WriteByte('\n')
		}
		line("architecturenote add", map[string]any{"title": "Courier client", "body": "The courier client wraps every outbound call of the billing worker. It keeps a pool of eight connections per host, signs each request with the tenant key and logs the span id. Its request_timeout defaults to 5000 ms; it retries twice on a 502 with jitter. Its retry_backoff defaults to 5000 ms between attempts. Metrics go to the shared collector under the courier prefix.", "meta": []string{"fact_state=live", "fact_verified_at=2026-11-20"}})
		line("memory add", map[string]any{"body": "Courier timeouts: there is no default for request_timeout, so every caller of the courier client must pass one."})
		line("memory add", map[string]any{"body": "Courier backoff: retry_backoff = 5 s unless a job overrides it."})
		line("memory add", map[string]any{"body": "Courier flushes: the flush_interval of the courier client is logged on start."})
		line("memory add", map[string]any{"body": "Courier flushes: raise flush_interval only with the queue owner."})
		batch := filepath.Join(t.TempDir(), "rows.jsonl")
		if err := os.WriteFile(batch, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		ada.Lore(pc.Dir, "batch", "apply", "--file", batch, "--yes").OK(t)
	})

	type asked struct {
		Rows []struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"rows"`
		Conflicts []struct {
			Field string `json:"field"`
			Rows  []struct {
				ID    string `json:"id"`
				Value string `json:"value"`
			} `json:"rows"`
		} `json:"conflicts"`
	}
	ask := func(q string) (asked, string) {
		var a asked
		r := ada.Lore(pc.Dir, "ask", "--query", q, "--json").OK(t)
		if err := json.Unmarshal([]byte(r.Out()), &a); err != nil {
			t.Fatalf("[O D-980] ask --json %q: %v\n%s", q, err, r)
		}
		return a, ada.Lore(pc.Dir, "ask", "--query", q).OK(t).Out()
	}
	idOf := func(a asked, prefix string) string {
		for _, r := range a.Rows {
			if strings.HasPrefix(r.Body, prefix) {
				return r.ID
			}
		}
		return ""
	}

	s.Step("D-980/contradicting-default-hint", func() {
		a, text := ask("what is the default request_timeout of the courier client")
		note, mem := idOf(a, "The courier client"), idOf(a, "Courier timeouts")
		if note == "" || mem == "" {
			t.Fatalf("[O D-980] ask did not print both rows:\n%s", text)
		}
		if len(a.Conflicts) != 1 || a.Conflicts[0].Field != "request_timeout" || len(a.Conflicts[0].Rows) != 2 ||
			a.Conflicts[0].Rows[0].ID != note || a.Conflicts[0].Rows[0].Value != "5000 ms" ||
			a.Conflicts[0].Rows[1].ID != mem || a.Conflicts[0].Rows[1].Value != "no default" {
			t.Fatalf("[O D-980] want one conflict on request_timeout, the live note first, got %+v", a.Conflicts)
		}
		want := "request_timeout: 5000 ms in " + note + ", no default in " + mem + "; lore show --id " + note + "; lore show --id " + mem
		if !strings.Contains(text, "rows that disagree") || !strings.Contains(text, want) {
			t.Fatalf("[O D-980] text has no conflict line %q:\n%s", want, text)
		}
		// the printed command works where it is printed
		ada.Lore(pc.Dir, "show", "--id", mem).OK(t).Says(t, "no default for request_timeout")
	})

	s.Step("D-980/agreeing-and-name-only-no-hint", func() {
		// the request_timeout pair is printed here too and still
		// disagrees; the fields asked about get no line
		for _, c := range []struct{ q, field string }{
			{"what is the courier client's retry_backoff", "retry_backoff"},
			{"what is the courier flush_interval", "flush_interval"},
		} {
			a, text := ask(c.q)
			if len(a.Rows) < 2 {
				t.Fatalf("[O D-980] %q: want two rows or more:\n%s", c.q, text)
			}
			for _, k := range a.Conflicts {
				if k.Field != "request_timeout" {
					t.Errorf("[O D-980] %q: want no conflict but request_timeout, got %+v:\n%s", c.q, a.Conflicts, text)
				}
			}
			if strings.Contains(text, "  "+c.field+":") {
				t.Errorf("[O D-980] %q: the text names %s as a conflict:\n%s", c.q, c.field, text)
			}
		}
	})
}
