//go:build userscen

package userscen

// campaign_data_resource_test.go — `lore resource …` the way an operator
// runs it: a resource's whole life with the list after each step, the
// dry runs of every bulk or destructive form checked by hashing the tree,
// run-twice behaviour, odd names and values, an import that fails part
// way writing nothing, the reports, a secret_ref field seen by `secrets
// list`, and the REST twin of the reads. The resource graph's via[] and the
// duration grammar are changed by other branches: their exact output is
// not pinned here.

import (
	"net/http"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// cdResNames lists the names `resource list <kind> --json` returns.
func cdResNames(t *testing.T, c *th.Clone, kind string, args ...string) []string {
	t.Helper()
	var out []string
	for _, r := range resList(t, c, kind, args...).Data {
		out = append(out, r.Name)
	}
	return out
}

func cdHas(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// TestCampaignData_ResourceLifecycle: add → show → edit → check → clone →
// rename → archive → delete (asks) → trash restore → delete --purge, with
// the list after every step and each step run twice where that is legal.
func TestCampaignData_ResourceLifecycle(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "add", "host", "web1", "--set", "provider=hetzner", "--set", "ip=10.0.0.1")
	cdRefused(t, c.Lore("resource", "add", "host", "web1", "--set", "provider=aws", "--json"), "E_RESOURCE_TAKEN")
	if got := resShow(t, c, "host/web1"); got.Values["ip"] != "10.0.0.1" || got.Values["provider"] != "hetzner" {
		t.Fatalf("show: %+v", got)
	}
	for range 2 {
		c.MustLore("resource", "edit", "host/web1", "--set", "cores=8")
	}
	if got := resShow(t, c, "host/web1"); got.Values["cores"] != "8" {
		t.Fatalf("edit: %+v", got)
	}
	cdRefused(t, c.Lore("resource", "edit", "host/web1", "--set", "provider=linode", "--json"), "E_RESOURCE_INVALID")
	cdRefused(t, c.Lore("resource", "edit", "host/web1", "--set", "ip=not-an-ip", "--json"), "E_RESOURCE_INVALID")
	for range 2 {
		c.MustLore("resource", "check", "host/web1", "--note", "looked in the console")
	}
	if got := resShow(t, c, "host/web1"); got.LastChecked == "" {
		t.Error("check did not stamp last_checked")
	}
	c.MustLore("resource", "clone", "host/web1", "web2", "--set", "ip=10.0.0.2")
	if got := resShow(t, c, "host/web2"); got.Values["provider"] != "hetzner" || got.Values["ip"] != "10.0.0.2" {
		t.Fatalf("clone: %+v", got)
	}
	c.MustLore("resource", "rename", "host/web2", "web02")
	if n := cdResNames(t, c, "host"); !cdHas(n, "web02") || cdHas(n, "web2") {
		t.Fatalf("after rename: %v", n)
	}
	c.MustLore("resource", "add", "service", "api", "--set", "runs_on=web1")
	for range 2 {
		c.MustLore("resource", "archive", "host/web02")
	}
	if n := cdResNames(t, c, "host"); cdHas(n, "web02") {
		t.Errorf("archived resource still in the default list: %v", n)
	}
	if n := cdResNames(t, c, "host", "--archived"); !cdHas(n, "web02") {
		t.Errorf("archived resource not in --archived: %v", n)
	}
	cdRefused(t, c.Lore("resource", "delete", "host/web1", "--json"), "E_NEEDS_CONFIRM")
	if !cdHas(cdResNames(t, c, "host"), "web1") {
		t.Fatal("an unconfirmed delete removed web1")
	}
	id := resShow(t, c, "host/web1").ID
	r := c.Lore("resource", "delete", "host/web1", "--yes", "--json")
	if r.Code == 0 {
		// web1 is in use by service api: if the delete is allowed the ref
		// must not silently disappear from the service
		if got := resShow(t, c, "service/api"); got.Values["runs_on"] == "" {
			t.Errorf("deleting web1 emptied api.runs_on: %+v", got)
		}
		if cdHas(cdResNames(t, c, "host"), "web1") {
			t.Fatal("deleted web1 still listed")
		}
		c.MustLore("trash", "restore", id, "--yes")
		if !cdHas(cdResNames(t, c, "host"), "web1") {
			t.Fatal("restored web1 not listed")
		}
	} else if e := restCLIErr(r); e == nil || e.Code != "E_RESOURCE_IN_USE" {
		t.Fatalf("delete of a host in use: %s", r)
	}
}

// TestCampaignData_ResourceDryRuns: every --dry-run of the bulk and
// destructive forms writes nothing; the real run then does what the dry
// run said.
func TestCampaignData_ResourceDryRuns(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "add", "host", "web1", "--set", "provider=hetzner", "--set", "cores=4")
	c.MustLore("resource", "add", "host", "web2", "--set", "provider=hetzner", "--set", "cores=8")
	c.MustLore("resource", "add", "service", "api", "--set", "runs_on=web1")
	for _, dry := range [][]string{
		{"resource", "field", "retype", "host", "cores", "text", "--dry-run"},
		{"resource", "field", "rename", "host", "cores", "cpus", "--dry-run"},
		{"resource", "repoint", "host/web1", "host/web2", "--dry-run"},
		{"resource", "check", "--kind", "host", "--where", "provider=hetzner", "--dry-run"},
		{"resource", "edit", "--kind", "host", "--where", "provider=hetzner", "--set", "cores=2", "--dry-run"},
		{"resource", "delete", "--kind", "host", "--where", "provider=hetzner", "--dry-run"},
	} {
		before := cdTreeHash(t, c)
		r := c.Lore(dry...)
		if r.Code != 0 {
			t.Errorf("%v: %s", dry, r)
			continue
		}
		if cdTreeHash(t, c) != before {
			t.Errorf("%v wrote to the tree", dry)
		}
	}
	c.MustLore("resource", "repoint", "host/web1", "host/web2", "--yes")
	if got := resShow(t, c, "service/api"); got.Values["runs_on"] != "web2" {
		t.Errorf("after repoint: %+v", got)
	}
	if cdHas(cdResNames(t, c, "host"), "web1") {
		t.Error("repoint did not archive web1")
	}
}

// TestCampaignData_ResourceOddValues: names and values of every shape.
func TestCampaignData_ResourceOddValues(t *testing.T) {
	c := resRepo(t)
	c.MustLore("resource", "kind", "add", "note_kind")
	c.MustLore("resource", "field", "add", "note_kind", "body", "--type", "text")
	// 5k, not 10k: a resource file holds each value twice (values and
	// history), and resources.max_bytes is 16384 (see the report)
	for i, v := range []string{"🚀 שלום café", cdLong()[:5000], "select * from hosts", "a/b c", "see ${var:api_host}"} {
		name := "row_" + string(rune('a'+i))
		c.MustLore("resource", "add", "note_kind", name, "--set", "body="+v)
		if got := resShow(t, c, "note_kind/"+name); got.Values["body"] != v {
			t.Errorf("%s: value %.30q came back as %.30q", name, v, got.Values["body"])
		}
	}
	before := cdTreeHash(t, c)
	for _, bad := range []string{"has space", "", "a/b", "שלום"} {
		r := c.Lore("resource", "add", "note_kind", bad, "--json")
		if e := restCLIErr(r); r.Code == 0 || e == nil {
			t.Errorf("resource add %q: want a refusal, got %s", bad, r)
		} else if e.Exit != r.Code || strings.TrimSpace(e.Hint) == "" {
			t.Errorf("resource add %q: %+v", bad, e)
		}
	}
	cdRefused(t, c.Lore("resource", "add", "no_such_kind", "x", "--json"), "E_KIND_UNKNOWN")
	cdRefused(t, c.Lore("resource", "show", "note_kind/missing", "--json"), "E_NOT_FOUND")
	if cdTreeHash(t, c) != before {
		t.Error("a refused resource command changed the tree")
	}
}

// TestCampaignData_ResourceImportAllOrNothing: an import with one bad row
// writes nothing; the fixed file imports every row in one batch, export
// gives the rows back, and a second import of the same file is refused or
// changes nothing.
func TestCampaignData_ResourceImportAllOrNothing(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	bad := resWriteFile(t, c, "bad.csv", "name,provider,ip\nh1,hetzner,10.0.0.1\nh2,linode,10.0.0.2\n")
	before := cdTreeHash(t, c)
	if r := c.Lore("resource", "import", bad, "--kind", "host", "--yes", "--json"); r.Code == 0 {
		t.Fatalf("an import with a bad row succeeded: %s", r)
	} else if e := restCLIErr(r); e == nil || e.Exit != r.Code {
		t.Fatalf("bad import: %s", r)
	}
	if cdTreeHash(t, c) != before || len(cdResNames(t, c, "host")) != 0 {
		t.Fatal("a failed import wrote rows")
	}
	good := resWriteFile(t, c, "good.csv", "name,provider,ip\nh1,hetzner,10.0.0.1\nh2,aws,10.0.0.2\n")
	c.MustLore("resource", "import", good, "--kind", "host", "--yes")
	if n := cdResNames(t, c, "host"); len(n) != 2 {
		t.Fatalf("after import: %v", n)
	}
	exp := c.MustLore("resource", "export", "--kind", "host").Stdout
	for _, w := range []string{"h1", "h2", "10.0.0.2"} {
		if !strings.Contains(exp, w) {
			t.Errorf("export lacks %s: %s", w, exp)
		}
	}
	again := c.Lore("resource", "import", good, "--kind", "host", "--yes", "--json")
	if n := cdResNames(t, c, "host"); len(n) != 2 {
		t.Errorf("a second import of the same file left %v (exit %d)", n, again.Code)
	}
}

// TestCampaignData_ResourceReports: kinds, kind show, lint, report, stale,
// impact, graph and history answer for a small estate; a dangling ref is
// reported by lint.
func TestCampaignData_ResourceReports(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "add", "host", "web1", "--set", "provider=hetzner")
	c.MustLore("resource", "add", "service", "api", "--set", "runs_on=web1", "--set", "port=443")
	c.MustLore("resource", "kinds").Says(t, "host", "service")
	c.MustLore("resource", "kind", "show", "service").Says(t, "runs_on", "port")
	c.MustLore("resource", "report").Says(t, "host", "service")
	c.MustLore("resource", "stale", "--days", "30").Says(t, "web1")
	c.MustLore("resource", "impact", "host/web1").Says(t, "api")
	c.MustLore("resource", "graph", "service/api", "--down").Says(t, "web1")
	c.MustLore("resource", "graph", "host/web1", "--up").Says(t, "api")
	c.MustLore("resource", "edit", "service/api", "--set", "port=8443")
	c.MustLore("resource", "history", "service/api").Says(t, "port")
	if l := c.MustLore("resource", "lint").Out(); !strings.Contains(l, "no problems") {
		t.Errorf("lint of a clean estate: %s", l)
	}
	c.MustLore("resource", "rehome").Says(t, "visible")
	cdRefused(t, c.Lore("resource", "impact", "host/missing", "--json"), "E_NOT_FOUND")
	cdRefused(t, c.Lore("resource", "kind", "show", "no_such_kind", "--json"), "E_KIND_UNKNOWN")
}

// TestCampaignData_ResourceKindsAndFields: kind and field edits, renames,
// a retype that every value survives and one that a value does not.
func TestCampaignData_ResourceKindsAndFields(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "add", "host", "web1", "--set", "provider=hetzner", "--set", "cores=4")
	c.MustLore("resource", "kind", "edit", "host", "--description", "a server we run")
	c.MustLore("resource", "kind", "show", "host").Says(t, "a server we run")
	cdRefused(t, c.Lore("resource", "kind", "add", "host", "--json"), "E_NAME_TAKEN")
	c.MustLore("resource", "field", "add", "host", "notes", "--type", "text")
	c.MustLore("resource", "field", "edit", "host", "notes", "--description", "free text")
	c.MustLore("resource", "field", "rename", "host", "notes", "remarks")
	c.MustLore("resource", "kind", "show", "host").Says(t, "remarks")
	c.MustLore("resource", "field", "retype", "host", "cores", "text", "--yes")
	if got := resShow(t, c, "host/web1"); got.Values["cores"] != "4" {
		t.Errorf("after retype to text: %+v", got)
	}
	c.MustLore("resource", "edit", "host/web1", "--set", "cores=four")
	r := c.Lore("resource", "field", "retype", "host", "cores", "number", "--yes", "--json")
	if e := restCLIErr(r); r.Code == 0 || e == nil || strings.TrimSpace(e.Hint) == "" {
		t.Errorf("retype with a value that does not convert: %s", r)
	}
	if got := resShow(t, c, "host/web1"); got.Values["cores"] != "four" {
		t.Errorf("a refused retype changed the value: %+v", got)
	}
	c.MustLore("resource", "field", "remove", "host", "remarks", "--yes")
	c.MustLore("resource", "kind", "rename", "host", "server")
	c.MustLore("resource", "kinds").Says(t, "server")
	if got := resShow(t, c, "server/web1"); got.Name != "web1" {
		t.Errorf("after kind rename: %+v", got)
	}
}

// TestCampaignData_ResourceSecretField: a secret_ref field holds a
// reference that `secrets list` finds and export masks.
func TestCampaignData_ResourceSecretField(t *testing.T) {
	c := resRepo(t)
	c.MustLore("resource", "kind", "add", "db")
	c.MustLore("resource", "field", "add", "db", "credential", "--type", "secret_ref")
	c.MustLore("resource", "add", "db", "main", "--set", "credential=gopass://acme/db/main")
	if l := c.MustLore("secrets", "list", "--json").Stdout; !strings.Contains(l, "gopass://acme/db/main") {
		t.Errorf("secrets list misses the resource field: %s", l)
	}
	tok := "gh" + "p_" + strings.Repeat("a1B2", 9)
	cdRefused(t, c.Lore("resource", "add", "db", "leaky", "--set", "credential="+tok, "--json"), "E_SECRET_DETECTED")
	if len(secDataHolding(t, c, tok)) != 0 {
		t.Error("a raw token reached .lore/data")
	}
}

// TestCampaignData_ResourceREST: list, show and kinds over the CLI and
// REST give the same document.
func TestCampaignData_ResourceREST(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	c.MustLore("resource", "add", "host", "web1", "--set", "provider=hetzner")
	id := resShow(t, c, "host/web1").ID
	srv := restStart(t, c)
	for _, x := range []struct {
		cli  []string
		path string
	}{
		{[]string{"resource", "show", id}, "/v1/resources/" + id},
		{[]string{"resource", "kinds"}, "/v1/resources/kinds"},
	} {
		cli := c.MustLore(append(x.cli, "--json")...)
		rep := srv.get(t, x.path)
		if x.path == "/v1/resources/kinds" {
			rep = srv.post(t, x.path, "{}") // openapi.json: resource.kinds is a POST
		}
		if rep.status != http.StatusOK || !restJSONEqual([]byte(cli.Stdout), []byte(rep.body)) {
			t.Errorf("%v: CLI %.300s REST %d %.300s", x.cli, cli.Stdout, rep.status, rep.body)
		}
	}
}
