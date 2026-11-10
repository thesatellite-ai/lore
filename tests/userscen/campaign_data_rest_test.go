//go:build userscen

package userscen

// campaign_data_rest_test.go — the same bad input to a data-area write
// over the CLI and over `lore api` gives the same error code, message and
// exit, and the REST status is the one of that code's class; the same good
// input over REST writes what the CLI reads back.

import (
	"net/http"
	"strings"
	"testing"
)

// TestCampaignData_RESTRefusalParity: one bad input per noun, both ways.
func TestCampaignData_RESTRefusalParity(t *testing.T) {
	c := resRepo(t)
	resHostService(t, c)
	m := cdMemory(t, c, "parity")
	srv := restStart(t, c)
	for _, x := range []struct {
		cli        []string
		path, body string
	}{
		{[]string{"tag", "add", "--name=has space"}, "/v1/tags", `{"name":"has space"}`},
		{[]string{"vars", "set", "Bad Name", "x"}, "/v1/vars/set", `{"name":"Bad Name","value":"x"}`},
		{[]string{"external-source", "add", "--name", "x", "--kind", "github", "--config", "{nope"}, "/v1/external_sources", `{"name":"x","kind":"github","config":"{nope"}`},
		{[]string{"comment", "add", "--on-table=memories", "--on-id=" + m, "--body="}, "/v1/comments", `{"on_table":"memories","on_id":"` + m + `","body":""}`},
		{[]string{"resource", "add", "host", "web1", "--set", "provider=linode"}, "/v1/resources", `{"kind":"host","name":"web1","set":["provider=linode"]}`},
	} {
		cli := c.Lore(append(x.cli, "--json")...)
		ce := restCLIErr(cli)
		rep := srv.post(t, x.path, x.body)
		if ce == nil || rep.err == nil {
			t.Errorf("%v: CLI %s / REST %d %.300s", x.cli, cli, rep.status, rep.body)
			continue
		}
		if ce.Code != rep.err.Code || ce.Exit != rep.err.Exit || ce.Message != rep.err.Message {
			t.Errorf("%v: CLI %s %q (exit %d) vs REST %s %q (exit %d)", x.cli, ce.Code, ce.Message, ce.Exit, rep.err.Code, rep.err.Message, rep.err.Exit)
		}
		if cli.Code != ce.Exit {
			t.Errorf("%v: process exit %d, envelope %d", x.cli, cli.Code, ce.Exit)
		}
		if rep.status < 400 || rep.status >= 500 {
			t.Errorf("%v: REST status %d for %s", x.cli, rep.status, rep.err.Code)
		}
	}
	// a good write over REST reads back over the CLI
	if rep := srv.post(t, "/v1/vars/set", `{"name":"api_host","value":"api.acme.example"}`); rep.status >= 300 {
		t.Fatalf("REST vars set: %d %.300s", rep.status, rep.body)
	}
	if v := cdVarValue(t, c, "api_host"); v != "api.acme.example" {
		t.Errorf("vars set over REST, CLI reads %q", v)
	}
	if rep := srv.post(t, "/v1/tags", `{"name":"from_rest"}`); rep.status != http.StatusCreated && rep.status != http.StatusOK {
		t.Fatalf("REST tag add: %d %.300s", rep.status, rep.body)
	}
	if !strings.Contains(c.MustLore("tag", "list").Stdout, "from_rest") {
		t.Error("a tag added over REST is not listed by the CLI")
	}
}
