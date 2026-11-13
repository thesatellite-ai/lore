//go:build userscen

package userscen

// campaign_data_meta_test.go — `lore meta …` the way a person runs it: a
// key's whole life (add → set → show → describe → edit → value add →
// value rename → rename → export → import → unset → delete → forget) with
// keys listed after each step, refusals with their documented exits, odd
// values, metadata search through a noun list, the dry runs that write
// nothing, and the REST twin of the reads.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// cdMetaShow is `lore meta show --id <id> --json`.
type cdMetaShow struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
	Data  []struct {
		Key   string `json:"key"`
		KeyID string `json:"key_id"`
		Scope string `json:"scope"`
		Value string `json:"value"`
	} `json:"data"`
}

func cdMetaOf(t *testing.T, c *th.Clone, id string) map[string]string {
	t.Helper()
	m := th.DecodeJSON[cdMetaShow](t, c.Lore("meta", "show", id, "--json"))
	out := map[string]string{}
	for _, d := range m.Data {
		out[d.Key] = d.Value
	}
	return out
}

// cdMemory adds a memory and returns its id.
func cdMemory(t *testing.T, c *th.Clone, body string) string {
	t.Helper()
	return cdIDOf(t, c.Lore("memory", "add", "--body", body, "--json"))
}

// TestCampaignData_MetaExportAfterRename: after `meta rename` and `meta
// value rename`, the export names the key and the value as the registry
// does now, and importing that export changes nothing — before the fix the
// export carried the row's stored labels, and the import filed them under
// a new key of the old name.
func TestCampaignData_MetaExportAfterRename(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "a memory with a tier")
	c.MustLore("meta", "add", "tier", "--type", "text")
	c.MustLore("meta", "value", "add", "tier", "gold")
	c.MustLore("meta", "value", "add", "tier", "silver")
	c.MustLore("meta", "set", m, "tier=gold")
	c.MustLore("meta", "value", "rename", "tier", "gold", "platinum")
	c.MustLore("meta", "rename", "tier", "level")
	for _, format := range []string{"csv", "json"} {
		r := c.MustLore("meta", "export", "--format", format)
		if strings.Contains(r.Stdout, "tier") || strings.Contains(r.Stdout, "gold") {
			t.Errorf("%s export names the old key or value: %s", format, r.Stdout)
		}
		if !strings.Contains(r.Stdout, "level") || !strings.Contains(r.Stdout, "platinum") {
			t.Errorf("%s export lacks the current names: %s", format, r.Stdout)
		}
	}
	csv := c.MustLore("meta", "export").Stdout
	p := filepath.Join(c.A.Home, "meta.csv")
	if err := os.WriteFile(p, []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	c.MustLore("meta", "import", p)
	keys := c.MustLore("meta", "keys").Stdout
	if strings.Contains(keys, "tier") {
		t.Errorf("re-importing the export added a key of the old name:\n%s", keys)
	}
	// the row's own labels may lag (docs: variables-and-metadata, "a value
	// label shown by meta get can lag"); what must hold is one key id
	if got := th.DecodeJSON[cdMetaShow](t, c.Lore("meta", "show", m, "--json")); got.Count != 1 {
		t.Errorf("after the round trip the row holds %d keys, want 1: %+v", got.Count, got.Data)
	}
}

// TestCampaignData_MetaLifecycle walks one key through every step and
// lists the keys after each.
func TestCampaignData_MetaLifecycle(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "the billing memory")
	task := cdIDOf(t, c.Lore("task", "add", "bill acme", "--to", "repo", "--commitment", "accepted", "--json"))
	keys := func() string { return c.MustLore("meta", "keys").Stdout }

	c.MustLore("meta", "add", "billing_plan", "--type", "text").Says(t, "billing_plan")
	if !strings.Contains(keys(), "billing_plan") {
		t.Fatal("added key not in meta keys")
	}
	cdRefused(t, c.Lore("meta", "add", "billing_plan", "--type", "text", "--json"), "E_META_TAKEN")
	c.MustLore("meta", "value", "add", "billing_plan", "pro")
	c.MustLore("meta", "value", "add", "billing_plan", "enterprise")
	for _, id := range []string{m, task} {
		c.MustLore("meta", "set", id, "billing_plan=pro")
		c.MustLore("meta", "set", id, "billing_plan=pro") // twice: same state
		if got := cdMetaOf(t, c, id); got["billing_plan"] != "pro" {
			t.Fatalf("meta show %s: %v", id, got)
		}
		if g := c.MustLore("meta", "get", id, "billing_plan").Stdout; !strings.Contains(g, "pro") {
			t.Errorf("meta get %s billing_plan: %q", id, g)
		}
	}
	c.MustLore("meta", "describe", "billing_plan", "the plan the client pays for")
	if !strings.Contains(c.MustLore("meta", "keys", "--json").Stdout, "the plan the client pays for") {
		t.Error("description not in meta keys --json")
	}
	c.MustLore("meta", "edit", "billing_plan", "--strict")
	cdRefused(t, c.Lore("meta", "set", m, "billing_plan=free", "--json"), "E_META_VALUE")
	if got := cdMetaOf(t, c, m); got["billing_plan"] != "pro" {
		t.Fatalf("a refused set changed the row: %v", got)
	}
	// search: a noun list narrowed by metadata finds exactly the row
	lst := c.MustLore("memory", "list", "--meta", "billing_plan=pro", "--json").Stdout
	if !strings.Contains(lst, m) {
		t.Errorf("memory list --meta billing_plan=pro misses %s: %.400s", m, lst)
	}
	if other := c.MustLore("memory", "list", "--meta", "billing_plan=enterprise", "--json").Stdout; strings.Contains(other, m) {
		t.Errorf("memory list --meta billing_plan=enterprise lists %s", m)
	}
	c.MustLore("meta", "value", "rename", "billing_plan", "pro", "professional")
	if !strings.Contains(keys(), "professional") {
		t.Error("renamed value not in meta keys")
	}
	c.MustLore("meta", "rename", "billing_plan", "plan", "--rewrite-rows")
	if k := keys(); !strings.Contains(k, "plan") || strings.Contains(k, "billing_plan") {
		t.Errorf("meta keys after rename:\n%s", k)
	}
	if got := cdMetaOf(t, c, task); got["plan"] == "" {
		t.Errorf("after rename --rewrite-rows the row shows %v", got)
	}
	if lint := c.MustLore("meta", "lint").Stdout; !strings.Contains(lint, "no metadata problems") {
		t.Errorf("meta lint after renames: %s", lint)
	}
	c.MustLore("meta", "unset", m, "plan")
	c.MustLore("meta", "unset", task, "plan")
	cdRefused(t, c.Lore("meta", "unset", m, "plan", "--json"), "E_META_UNKNOWN")
	cdRefused(t, c.Lore("meta", "delete", "plan", "--json"), "E_NEEDS_CONFIRM")
	if !strings.Contains(keys(), "plan") {
		t.Fatal("an unconfirmed delete removed the key")
	}
	c.MustLore("meta", "delete", "plan", "--yes")
	if strings.Contains(keys(), "plan") {
		t.Error("deleted key still listed")
	}
}

// TestCampaignData_MetaLintTextBeforeValue: a value set as text before
// the key got that allowed value is reported as such (not as "not one of
// its allowed values", which it is), with a fix that stores the value id;
// after the fix lint is clean.
func TestCampaignData_MetaLintTextBeforeValue(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "set before the value existed")
	c.MustLore("meta", "set", m, "tier=gold")
	c.MustLore("meta", "value", "add", "tier", "gold")
	lint := c.MustLore("meta", "lint").Stdout
	if !strings.Contains(lint, "written as text before it became an allowed value") || !strings.Contains(lint, "lore meta set --id "+m+" --pair tier=gold") {
		t.Fatalf("meta lint: %s", lint)
	}
	c.MustLore("meta", "set", m, "tier=gold")
	if lint := c.MustLore("meta", "lint").Stdout; !strings.Contains(lint, "no metadata problems") {
		t.Errorf("after the fix: %s", lint)
	}
	c.MustLore("meta", "value", "add", "tier", "silver")
	c.MustLore("meta", "set", m, "tier=silver")
	c.MustLore("meta", "value", "rename", "tier", "silver", "platinum")
	if lint := c.MustLore("meta", "lint").Stdout; !strings.Contains(lint, "no metadata problems") {
		t.Errorf("after a value rename: %s", lint)
	}
}

// TestCampaignData_MetaUnsetDryRun: `meta unset <id> <key> --dry-run`
// names what it would remove and writes nothing (it removed the key); a
// key the row lacks is refused as the real run refuses it.
func TestCampaignData_MetaUnsetDryRun(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "keep my owner")
	c.MustLore("meta", "set", m, "owner=alice")
	before := cdTreeHash(t, c)
	c.MustLore("meta", "unset", m, "owner", "--dry-run").Says(t, "would remove owner", "nothing written")
	if cdTreeHash(t, c) != before {
		t.Fatal("meta unset --dry-run changed the tree")
	}
	if got := cdMetaOf(t, c, m); got["owner"] != "alice" {
		t.Fatalf("meta unset --dry-run removed the key: %v", got)
	}
	cdRefused(t, c.Lore("meta", "unset", m, "team", "--dry-run", "--json"), "E_META_UNKNOWN")
	c.MustLore("meta", "unset", m, "owner")
	if got := cdMetaOf(t, c, m); got["owner"] != "" {
		t.Errorf("real unset kept the key: %v", got)
	}
}

// TestCampaignData_MetaOddValues: what a value may hold (unicode, RTL,
// 10k characters, a ${var:…} reference) round-trips byte for byte; bad key
// names are refused with their documented codes and nothing is written.
func TestCampaignData_MetaOddValues(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "odd values")
	c.MustLore("vars", "set", "api_host", "api.acme.example")
	for _, v := range []string{"café ✓ 🚀", "שלום עולם", cdLong(), "see ${var:api_host}", "select", "a/b c"} {
		c.MustLore("meta", "set", m, "odd_value="+v)
		if got := cdMetaOf(t, c, m)["odd_value"]; got != v {
			t.Errorf("value %.40q came back as %.40q", v, got)
		}
	}
	before := cdTreeHash(t, c)
	for _, bad := range []struct{ arg, code string }{
		{"Has Space=x", "E_META_KEY"},
		{"=x", "E_META_OP"},
		{"color=x", "E_META_RESERVED"},
	} {
		r := c.Lore("meta", "set", m, bad.arg, "--json")
		if e := restCLIErr(r); r.Code == 0 || e == nil || !strings.HasPrefix(e.Code, "E_") {
			t.Errorf("meta set %q: want a refusal, got %s", bad.arg, r)
		} else if e.Code != bad.code {
			t.Errorf("meta set %q: want %s, got %s (%s)", bad.arg, bad.code, e.Code, e.Message)
		}
	}
	cdRefused(t, c.Lore("meta", "show", "mem_01a1b7b91e947cd6802bd13a54250000", "--json"), "E_NOT_FOUND")
	if cdTreeHash(t, c) != before {
		t.Error("a refused meta command changed the tree")
	}
}

// TestCampaignData_MetaDryRuns: import --dry-run and unset --dry-run
// write nothing, the real runs do, and a batch import can be undone.
func TestCampaignData_MetaDryRuns(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "dry runs")
	c.MustLore("meta", "set", m, "owner=alice")
	p := filepath.Join(c.A.Home, "in.csv")
	if err := os.WriteFile(p, []byte("id,key,value\n"+m+",team,platform\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := cdTreeHash(t, c)
	c.MustLore("meta", "import", p, "--dry-run").Says(t, "nothing written")
	c.MustLore("meta", "unset", m, "owner", "--dry-run")
	if cdTreeHash(t, c) != before {
		t.Fatal("a dry run changed the tree")
	}
	if got := cdMetaOf(t, c, m); got["owner"] != "alice" || got["team"] != "" {
		t.Fatalf("dry runs changed the row: %v", got)
	}
	imp := c.MustLore("meta", "import", p, "--json").Stdout
	if got := cdMetaOf(t, c, m); got["team"] != "platform" {
		t.Fatalf("import did not set team: %v", got)
	}
	var out struct {
		ID string `json:"id"` // the bat_ id of the import
	}
	if err := json.Unmarshal([]byte(imp), &out); err != nil || !strings.HasPrefix(out.ID, "bat_") {
		t.Fatalf("import --json names no batch: %s", imp)
	}
	c.MustLore("batch", "undo", out.ID, "--yes")
	if got := cdMetaOf(t, c, m); got["team"] != "" {
		t.Errorf("batch undo kept team: %v", got)
	}
}

// TestCampaignData_MetaREST: meta keys and meta show answer the same
// document over the CLI and `lore api`.
func TestCampaignData_MetaREST(t *testing.T) {
	c := resRepo(t)
	m := cdMemory(t, c, "rest twin")
	c.MustLore("meta", "set", m, "owner=alice")
	srv := restStart(t, c)
	for _, x := range []struct {
		cli  []string
		path string
	}{
		{[]string{"meta", "keys"}, "/v1/meta/keys"},
		{[]string{"meta", "show", m}, "/v1/metas/" + m},
	} {
		cli := c.MustLore(append(x.cli, "--json")...)
		rep := srv.get(t, x.path)
		if x.path == "/v1/meta/keys" {
			rep = srv.post(t, x.path, "{}") // openapi.json: meta.keys is a POST
		}
		if rep.status != http.StatusOK {
			t.Errorf("GET %s: %d %.300s", x.path, rep.status, rep.body)
			continue
		}
		if !restJSONEqual([]byte(cli.Stdout), []byte(rep.body)) {
			t.Errorf("%v: CLI and REST differ\nCLI:  %.400s\nREST: %.400s", x.cli, cli.Stdout, rep.body)
		}
	}
}
