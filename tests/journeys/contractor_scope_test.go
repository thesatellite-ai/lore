//go:build journeys && !windows

package journeys

// contractor_scope_test.go — J-15: a contractor writes only to their
// projects' folders. The admin of acme invites Pat as a contractor; after
// joining, Pat's company-wide rule (--to acme/org) and rename of the
// company's team are refused with one line naming the contractor stint,
// and nothing reaches the store. The admin's own team rename still works.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyContractorScope(t *testing.T) {
	w := NewWorld(t)
	pat := w.Persona(jJoinerName, jJoinerWorkEmail)
	pm := pat.Laptop()
	pm.GitHubAccounts(t, GitHubAccount{Login: jJoinerLoginA, Key: "id_ed25519"})
	boss := w.Persona("Boss Acme", jJoinerBossEmail)
	bm := boss.Laptop()
	bm.GitHubAccounts(t, GitHubAccount{Login: jJoinerLoginBoss, Key: "id_ed25519"})
	appRemote := w.Remote("github.com/acme-co/app")
	teamRemote := w.Remote("github.com/acme-co/lore-team")
	var invite string
	bwork := filepath.Join(w.Root, "work", bm.Name)
	pwork := filepath.Join(w.Root, "work", pm.Name)
	const companyRule = "probe: contractor writes a company rule"

	w.Step("admin/invites-a-contractor", func() {
		bapp := bm.Clone(appRemote, "app")
		for _, a := range []jJoinerAnswer{
			{"kind", map[string]any{"kind": "admin", "name": "acme"}},
			{"identity", map[string]any{"name": "Boss Acme", "email": jJoinerBossEmail}},
			{"github", map[string]any{"github_account": jJoinerLoginBoss, "host_alias": "github.com"}},
			{"personal_store", map[string]any{"personal_store": "none"}},
			{"team", map[string]any{"org": jJoinerOrg, "store_url": teamRemote.SSHURL()}},
			{"tools", map[string]any{"tools": []string{"claude_code"}}},
			{"secrets", map[string]any{"backend": "none"}},
			{"folders", map[string]any{"folders": []string{bwork + "/**"}, "default": true}},
			{"summary", map[string]any{}},
		} {
			jJoinerApply(t, bm, bapp.Dir, "acme", a)
		}
		bm.LoreIn(bapp, "team", "add", "--handle", "@payments", "--members", "@"+strings.Split(jJoinerBossEmail, "@")[0]).OK(t)
		r := bm.LoreIn(bapp, "team", "invite", jJoinerWorkEmail, "--type", "contractor", "--yes", "--json").OK(t)
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

	w.Step("contractor/joins", func() {
		if err := os.MkdirAll(pwork, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, a := range []jJoinerAnswer{
			{"kind", map[string]any{"kind": "member", "name": jJoinerSetupA}},
			{"identity", map[string]any{"name": jJoinerName, "email": jJoinerWorkEmail}},
			{"github", map[string]any{"github_account": jJoinerLoginA, "host_alias": "github.com"}},
			{"personal_store", map[string]any{"personal_store": "none"}},
			{"join", map[string]any{"invite": invite}},
			{"tools", map[string]any{"tools": []string{"claude_code"}}},
			{"secrets", map[string]any{"backend": "none"}},
			{"folders", map[string]any{"folders": []string{pwork + "/**"}, "default": true}},
			{"summary", map[string]any{}},
		} {
			jJoinerApply(t, pm, "", jJoinerSetupA, a)
		}
		pm.Lore(pwork, "sync").OK(t)
	})

	w.Step("contractor/company-writes-refused", func() {
		pm.Lore(pwork, "rule", "add", "--to", jJoinerOrg+"/org", "--body", companyRule, "--severity", "should").
			Refused(t).Says(t, "contractor in "+jJoinerOrg)
		pm.Lore(pwork, "team", "rename", "--handle", "@payments", "--to", "@probe", "--yes").
			Refused(t).Says(t, "contractor in "+jJoinerOrg)
		pm.Lore(pwork, "sync").OK(t)
		if jJoinerRemoteHas(w, teamRemote, companyRule) || jJoinerRemoteHas(w, teamRemote, "\"probe\"") {
			t.Error("the contractor's company write reached the store")
		}
	})

	w.Step("contractor/own-agent-starts", func() {
		// the contractor's own agent is their identity on the roster, like
		// their own register: the org folder lets it through
		pm.Lore(pwork, "agent", "start", "--label", "pm-fe").OK(t).Says(t, "agent pm-fe").Never(t, "contractor in "+jJoinerOrg)
	})

	w.Step("admin/still-renames", func() {
		bm.Lore(bwork, "team", "rename", "--handle", "@payments", "--to", "@billing", "--yes").OK(t)
	})
}
