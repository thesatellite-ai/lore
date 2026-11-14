//go:build journeys && !windows

package journeys

// join_takeover_test.go — J-9: an invite is used once, and nobody becomes
// the invitee by listing the invitee's address as an "other address".
// Pat accepts acme's invite. Ivy, a different person with the same line,
// lists Pat's address among her emails: setup check says the join is not
// ready and apply is refused (the primary address decides); claiming
// Pat's address as her own primary is refused too (the invite is used).
// Ivy's config never gets Pat's identity key.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	jtIntruderName  = "Ivy Probe"
	jtIntruderEmail = "ivy@elsewhere.example"
	jtIntruderLogin = "ivy-probe"
	jtSetup         = "ivyjob"
)

func TestJourneyJoinNoTakeover(t *testing.T) {
	w := NewWorld(t)
	pat := w.Persona(jJoinerName, jJoinerWorkEmail)
	pm := pat.Laptop()
	pm.GitHubAccounts(t, GitHubAccount{Login: jJoinerLoginA, Key: "id_ed25519"})
	boss := w.Persona("Boss Acme", jJoinerBossEmail)
	bm := boss.Laptop()
	bm.GitHubAccounts(t, GitHubAccount{Login: jJoinerLoginBoss, Key: "id_ed25519"})
	ivy := w.Persona(jtIntruderName, jtIntruderEmail)
	im := ivy.Laptop()
	im.GitHubAccounts(t, GitHubAccount{Login: jtIntruderLogin, Key: "id_ed25519"})
	appRemote := w.Remote("github.com/acme-co/app")
	teamRemote := w.Remote("github.com/acme-co/lore-team")
	var invite string

	w.Step("admin/invites-pat", func() {
		bapp := bm.Clone(appRemote, "app")
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

	w.Step("pat/joins", func() {
		for _, a := range []jJoinerAnswer{
			{"kind", map[string]any{"kind": "member", "name": jJoinerSetupA}},
			{"identity", map[string]any{"name": jJoinerName, "email": jJoinerWorkEmail}},
			{"github", map[string]any{"github_account": jJoinerLoginA, "host_alias": "github.com"}},
			{"personal_store", map[string]any{"personal_store": "none"}},
			{"join", map[string]any{"invite": invite}},
		} {
			jJoinerApply(t, pm, "", jJoinerSetupA, a)
		}
		// the join reaches the store with Pat's sync: the invite is accepted there
		work := filepath.Join(w.Root, "work", pm.Name)
		if err := os.MkdirAll(work, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, a := range []jJoinerAnswer{
			{"tools", map[string]any{"tools": []string{"claude_code"}}},
			{"secrets", map[string]any{"backend": "none"}},
			{"folders", map[string]any{"folders": []string{work + "/**"}, "default": true}},
			{"summary", map[string]any{}},
		} {
			jJoinerApply(t, pm, "", jJoinerSetupA, a)
		}
		pm.Lore(work, "sync").OK(t)
	})

	join, _ := json.Marshal(map[string]any{"invite": invite})
	ivyJoin := func(identity map[string]any, want ...string) {
		t.Helper()
		jJoinerApply(t, im, "", jtSetup, jJoinerAnswer{"identity", identity})
		c := im.Lore("", "setup", "check", jtSetup, "--step", "join", "--answers", string(join), "--json")
		if strings.Contains(c.Stdout, `"status": "ready"`) || strings.Contains(c.Stdout, `"status":"ready"`) {
			t.Errorf("setup check says ready for a join apply refuses: %s", c.Stdout)
		}
		r := im.Lore("", "setup", "apply", jtSetup, "--step", "join", "--answers", string(join), "--json")
		if r.Code == 0 {
			t.Fatalf("ivy joined with pat's invite: %s", r)
		}
		r.Says(t, want...)
	}

	w.Step("ivy/lists-pats-address-as-another", func() {
		for _, a := range []jJoinerAnswer{
			{"kind", map[string]any{"kind": "member", "name": jtSetup}},
			{"identity", map[string]any{"name": jtIntruderName, "email": jtIntruderEmail}},
			{"github", map[string]any{"github_account": jtIntruderLogin, "host_alias": "github.com"}},
			{"personal_store", map[string]any{"personal_store": "none"}},
		} {
			jJoinerApply(t, im, "", jtSetup, a)
		}
		ivyJoin(map[string]any{"name": jtIntruderName, "email": jtIntruderEmail, "emails": []string{jJoinerWorkEmail}}, "invite.email", "not your identity email")
	})

	w.Step("ivy/claims-pats-address-as-her-own", func() {
		ivyJoin(map[string]any{"name": jtIntruderName, "email": jJoinerWorkEmail, "emails": []string{jtIntruderEmail}}, "invite.status", "already used")
		b, err := os.ReadFile(filepath.Join(im.Home, ".lore", "config.yaml"))
		if err == nil && strings.Contains(string(b), "human:"+jJoinerWorkEmail) {
			t.Errorf("ivy's config holds pat's identity key:\n%s", b)
		}
	})
}
