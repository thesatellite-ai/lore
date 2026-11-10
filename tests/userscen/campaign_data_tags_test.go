//go:build userscen

package userscen

// campaign_data_tags_test.go — `lore tag …`, `lore pin …` and `lore
// external-source …` the way a person runs them: names of every shape,
// attach/detach run twice, edit then attach by the new name, pins on the
// rows that take them and refusals on the rows that do not, sources
// enabled and disabled twice, every refusal with its documented exit and a
// next step, and the REST twin of the lists.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// cdTagUses is the uses count `tag list` prints for name ("(N uses)").
func cdTagUses(t *testing.T, list, name string) string {
	t.Helper()
	for _, l := range strings.Split(list, "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == name {
			return strings.TrimPrefix(f[len(f)-2], "(")
		}
	}
	t.Fatalf("tag %s not listed:\n%s", name, list)
	return ""
}

// TestCampaignData_TagNames: the name matrix of tag add.
func TestCampaignData_TagNames(t *testing.T) {
	c := resRepo(t)
	for _, n := range []struct{ name, code string }{
		{"cert_rotation", ""},
		{"select", ""}, // an SQL word is a name like any other
		{"v2_api", ""},
		{"cert_rotation", "E_NAME_TAKEN"},
		{"has space", "E_INVALID_IDENTIFIER"},
		{"a/b", "E_INVALID_IDENTIFIER"},
		{"Upper", "E_INVALID_IDENTIFIER"},
		{"été", "E_INVALID_IDENTIFIER"},
		{"שלום", "E_INVALID_IDENTIFIER"},
		{"🚀", "E_INVALID_IDENTIFIER"},
		{"", "E_INVALID_INPUT"},
	} {
		r := c.Lore("tag", "add", "--name="+n.name, "--json")
		if n.code == "" {
			r.OK(t)
			continue
		}
		cdRefused(t, r, n.code)
	}
	// a name with letters outside a-z says the suggestion drops them
	// (été suggested a bare "t"; שלום suggested an empty name)
	for _, n := range []string{"été", "שלום", "Café_x"} {
		e := restCLIErr(c.Lore("tag", "add", "--name="+n, "--json"))
		if e == nil || !strings.Contains(e.Message, "ASCII") || strings.HasSuffix(e.Message, "Use: ") {
			t.Errorf("tag add %q: %+v", n, e)
		}
	}
	list := c.MustLore("tag", "list").Stdout
	for _, n := range []string{"cert_rotation", "select", "v2_api"} {
		cdTagUses(t, list, n)
	}
	if strings.Count(list, "cert_rotation") != 1 {
		t.Errorf("duplicate tag listed:\n%s", list)
	}
}

// TestCampaignData_TagAttachLifecycle: attach twice (one use), detach
// twice (zero uses, no error), edit the name, attach by the new name, the
// old name is not found.
func TestCampaignData_TagAttachLifecycle(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "tag me")
	c.MustLore("tag", "add", "--name=cert_rotation")
	on := []string{"--on-table", "memories", "--on-id", m}
	for range 2 {
		c.MustLore(append([]string{"tag", "attach", "--tag", "cert_rotation"}, on...)...)
		if u := cdTagUses(t, c.MustLore("tag", "list").Stdout, "cert_rotation"); u != "1" {
			t.Fatalf("after attach: %s uses", u)
		}
	}
	for range 2 {
		c.MustLore(append([]string{"tag", "detach", "--tag", "cert_rotation"}, on...)...)
		if u := cdTagUses(t, c.MustLore("tag", "list").Stdout, "cert_rotation"); u != "0" {
			t.Fatalf("after detach: %s uses", u)
		}
	}
	c.MustLore("tag", "edit", "cert_rotation", "--name", "certs")
	c.MustLore(append([]string{"tag", "attach", "--tag", "certs"}, on...)...)
	cdRefused(t, c.Lore(append([]string{"tag", "attach", "--tag", "cert_rotation", "--json"}, on...)...), "E_NOT_FOUND")
	cdRefused(t, c.Lore("tag", "attach", "--tag", "certs", "--on-table", "memories", "--on-id", "mem_nope", "--json"), "E_INVALID_ID")
	cdRefused(t, c.Lore("tag", "attach", "--tag", "certs", "--on-table", "bogus", "--on-id", m, "--json"), "E_UNSUPPORTED")
	// REST twin of the list
	srv := restStart(t, c)
	cli := c.MustLore("tag", "list", "--json")
	if rep := srv.get(t, "/v1/tags"); rep.status != http.StatusOK || !restJSONEqual([]byte(cli.Stdout), []byte(rep.body)) {
		t.Errorf("tag list: CLI %.300s REST %d %.300s", cli.Stdout, rep.status, rep.body)
	}
}

// TestCampaignData_Pins: a must rule takes a pin (twice: same answer),
// switches context, unpins twice; a should rule and a memory are refused
// with a next step.
func TestCampaignData_Pins(t *testing.T) {
	c := resRepo(t)
	must := cdIDOf(t, c.Lore("rule", "add", "never push to main", "--severity", "must", "--json"))
	should := cdIDOf(t, c.Lore("rule", "add", "prefer small PRs", "--severity", "should", "--json"))
	m := cdMemory(t, c, "not pinnable")
	for _, ctx := range []string{"full", "full", "line"} {
		c.MustLore("pin", "set", must, "--context", ctx).Says(t, "pinned: context "+ctx)
	}
	cdRefused(t, c.Lore("pin", "set", must, "--context", "bogus", "--json"), "E_INVALID_INPUT")
	cdRefused(t, c.Lore("pin", "set", should, "--context", "full", "--json"), "E_INVALID_INPUT")
	e := cdRefused(t, c.Lore("pin", "set", m, "--context", "full", "--json"), "E_NOT_FOUND")
	if !strings.Contains(e.Hint, "rule") || strings.Contains(e.Hint, "sync peek") {
		t.Errorf("pin on a memory: the hint should point at rules and hotfixes: %q", e.Hint)
	}
	cdRefused(t, c.Lore("pin", "set", "--context", "full", "--json"), "E_INVALID_INPUT")
	for range 2 {
		c.MustLore("pin", "unset", must).Says(t, "unpinned")
	}
	srv := restStart(t, c)
	if rep := srv.post(t, "/v1/pins/"+must+"/set", `{"context":"line"}`); rep.status != http.StatusOK {
		t.Errorf("REST pin set: %d %.300s", rep.status, rep.body)
	}
}

// TestCampaignData_ExternalSources: add (disabled), list, enable twice,
// disable twice, refusals: bad JSON config, a name instead of the ext_ id
// (with a hint naming the list), a token in the config (asks first).
func TestCampaignData_ExternalSources(t *testing.T) {
	c := resRepo(t)
	if h := c.MustLore("external-source", "add", "--help").Stdout; strings.Contains(h, "(required) (required)") {
		t.Errorf("a flag says (required) twice:\n%s", h)
	}
	id := cdIDOf(t, c.Lore("external-source", "add", "--name", "issues", "--kind", "github", "--config", `{"repo":"acme/app"}`, "--json"))
	type src struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Enabled bool   `json:"enabled"`
	}
	state := func() src {
		var l struct {
			Kind string `json:"kind"`
			Data []src  `json:"data"`
		}
		if err := json.Unmarshal([]byte(c.MustLore("external-source", "list", "--json").Stdout), &l); err != nil {
			t.Fatal(err)
		}
		for _, s := range l.Data {
			if s.ID == id {
				return s
			}
		}
		t.Fatalf("source %s not listed", id)
		return src{}
	}
	if s := state(); s.Enabled || s.Name != "issues" || s.Kind != "github" {
		t.Fatalf("a new source: %+v", s)
	}
	for _, on := range []bool{true, true, false, false} {
		verb := "disable"
		if on {
			verb = "enable"
		}
		c.MustLore("external-source", verb, id)
		if s := state(); s.Enabled != on {
			t.Fatalf("after %s: %+v", verb, s)
		}
	}
	cdRefused(t, c.Lore("external-source", "add", "--name", "bad", "--kind", "github", "--config", "{not json", "--json"), "E_INVALID_INPUT")
	cdRefused(t, c.Lore("external-source", "add", "--kind", "github", "--json"), "E_INVALID_INPUT")
	// enable takes the unique name as well as the ext_ id (f5cc7c55,
	// TestCampaignDataFollowups_ExternalSourceNames); a name no source has
	// is E_NOT_FOUND whose hint names the list
	c.MustLore("external-source", "enable", "issues")
	if s := state(); !s.Enabled {
		t.Fatalf("enable by name: %+v", s)
	}
	e := cdRefused(t, c.Lore("external-source", "enable", "no-such-source", "--json"), "E_NOT_FOUND")
	if !strings.Contains(e.Hint, "lore external-source list") {
		t.Errorf("enable by an unknown name: the hint should name the list: %q", e.Hint)
	}
	tok := "gh" + "p_" + strings.Repeat("a1B2", 9)
	cdRefused(t, c.Lore("external-source", "add", "--name", "tok", "--kind", "github", "--config", `{"token":"`+tok+`"}`, "--json"), "E_NEEDS_CONFIRM")
	if len(secDataHolding(t, c, tok)) != 0 {
		t.Error("the refused token was written to .lore/data")
	}
	srv := restStart(t, c)
	cli := c.MustLore("external-source", "list", "--json")
	if rep := srv.get(t, "/v1/external_sources"); rep.status != http.StatusOK || !restJSONEqual([]byte(cli.Stdout), []byte(rep.body)) {
		t.Errorf("external-source list: CLI %.300s REST %d %.300s", cli.Stdout, rep.status, rep.body)
	}
}
