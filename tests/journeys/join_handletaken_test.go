//go:build journeys && !windows

package journeys

// join_handletaken_test.go — a join refused because the handle is taken is
// fixed from the setup flow (run:onboarding:open-4). The admin of acme has
// already put someone with the handle @pat on the roster; Pat accepts an
// invite on the setup page with the handle "pat", and the join step is
// refused naming the handle and who holds it. Pat changes the handle on
// the Identity step and saves the join again: it goes through, the roster
// has Pat as @patj with the invite's address, and the other @pat is
// untouched.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyJoinHandleTaken(t *testing.T) {
	w := NewWorld(t)
	pat := w.Persona(jJoinerName, jJoinerWorkEmail)
	m := pat.Laptop()
	m.GitHubAccounts(t, GitHubAccount{Login: jJoinerLoginA, Key: "id_ed25519"})
	boss := w.Persona("Boss Acme", jJoinerBossEmail)
	bm := boss.Laptop()
	bm.GitHubAccounts(t, GitHubAccount{Login: jJoinerLoginBoss, Key: "id_ed25519"})
	appRemote := w.Remote("github.com/acme-co/app")
	teamRemote := w.Remote("github.com/acme-co/lore-team")
	var invite string
	var bapp *teamharness.Clone

	w.Step("admin/handle-pat-is-taken", func() {
		bapp = bm.Clone(appRemote, "app")
		for _, a := range []jJoinerAnswer{
			{"kind", map[string]any{"kind": "admin", "name": "acme"}},
			{"identity", map[string]any{"name": "Boss Acme", "email": jJoinerBossEmail}},
			{"github", map[string]any{"github_account": jJoinerLoginBoss, "host_alias": "github.com"}},
			{"personal_store", map[string]any{"personal_store": "none"}},
			{"team", map[string]any{"org": jJoinerOrg, "store_url": teamRemote.SSHURL()}},
			{"tools", map[string]any{"tools": []string{"claude_code"}}},
			{"secrets", map[string]any{"backend": "none"}},
			{"folders", map[string]any{"folders": []string{filepath.Join(w.Root, "work", bm.Name) + "/**"}, "default": true}},
			{"summary", map[string]any{}},
		} {
			jJoinerApply(t, bm, bapp.Dir, "acme", a)
		}
		bm.LoreIn(bapp, "member", "add", "--email", "patricia@acme.example", "--name", "Patricia Old", "--handle", "pat", "--yes").OK(t)
		r := bm.LoreIn(bapp, "team", "invite", jJoinerWorkEmail, "--yes", "--json").OK(t)
		var o struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal([]byte(r.Stdout), &o)
		invite = o.Text
		if !strings.HasPrefix(invite, "lore-invite:") {
			t.Fatalf("team invite printed no invite line: %s", r)
		}
		bm.LoreIn(bapp, "sync").OK(t)
	})

	identity := func(handle string) jJoinerAnswer {
		return jJoinerAnswer{"identity", map[string]any{"name": jJoinerName, "email": jJoinerWorkEmail, "handle": handle}}
	}
	join := jJoinerAnswer{"join", map[string]any{"invite": invite}}

	w.Step("pat/join-refused-handle-taken", func() {
		for _, a := range []jJoinerAnswer{
			{"kind", map[string]any{"kind": "member", "name": jJoinerSetupA}},
			identity("pat"),
			{"github", map[string]any{"github_account": jJoinerLoginA, "host_alias": "github.com"}},
			{"personal_store", map[string]any{"personal_store": "none"}},
		} {
			jJoinerApply(t, m, "", jJoinerSetupA, a)
		}
		b, _ := json.Marshal(join.answers)
		r := m.Lore("", "setup", "apply", jJoinerSetupA, "--step", "join", "--answers", string(b), "--json")
		if r.Code == 0 {
			t.Fatalf("the join with a taken handle was accepted: %s", r)
		}
		r.Says(t, "@pat", "taken")
	})

	w.Step("pat/new-handle-on-identity-then-join", func() {
		jJoinerApply(t, m, "", jJoinerSetupA, identity("patj"))
		jJoinerApply(t, m, "", jJoinerSetupA, join)
		// Pat's sync sends the join (C5); the admin reads the roster: Pat
		// is @patj, Patricia keeps @pat
		work := filepath.Join(w.Root, "work", m.Name)
		if err := os.MkdirAll(work, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, a := range []jJoinerAnswer{
			{"tools", map[string]any{"tools": []string{"claude_code"}}},
			{"secrets", map[string]any{"backend": "none"}},
			{"folders", map[string]any{"folders": []string{work + "/**"}, "default": true}},
			{"summary", map[string]any{}},
		} {
			jJoinerApply(t, m, "", jJoinerSetupA, a)
		}
		m.Lore(work, "sync").OK(t)
		bm.LoreIn(bapp, "sync").OK(t)
		bm.LoreIn(bapp, "actor", "show", "--id", "@patj").OK(t).Says(t, jJoinerName, jJoinerWorkEmail)
		bm.LoreIn(bapp, "actor", "show", "--id", "@pat").OK(t).Says(t, "Patricia Old").Never(t, jJoinerWorkEmail)
	})
}
