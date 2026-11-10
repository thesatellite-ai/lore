//go:build userscen

package userscen

// campaign_data_schema_test.go — the --json of every read command of the
// data area against its response schema in docs/reference/openapi.json:
// every member printed is documented, every required member is printed,
// and each value has the documented type (null only where the schema
// allows it). The documents come from a repo holding one row of each noun.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// cdSchema is the subset of an OpenAPI schema the check reads.
type cdSchema struct {
	Type                 json.RawMessage      `json:"type"`
	Properties           map[string]*cdSchema `json:"properties"`
	Required             []string             `json:"required"`
	Items                *cdSchema            `json:"items"`
	AdditionalProperties json.RawMessage      `json:"additionalProperties"`
	Nullable             bool                 `json:"nullable"`
	OneOf                []*cdSchema          `json:"oneOf"`
	AnyOf                []*cdSchema          `json:"anyOf"`
}

// types is the schema's type list ("" when it names none).
func (s *cdSchema) types() []string {
	if len(s.Type) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(s.Type, &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(s.Type, &many)
	return many
}

// cdJSONType names the JSON type of raw.
func cdJSONType(raw json.RawMessage) string {
	t := strings.TrimSpace(string(raw))
	switch {
	case t == "null":
		return "null"
	case t == "true" || t == "false":
		return "boolean"
	case strings.HasPrefix(t, "{"):
		return "object"
	case strings.HasPrefix(t, "["):
		return "array"
	case strings.HasPrefix(t, `"`):
		return "string"
	}
	if !strings.ContainsAny(t, ".eE") {
		return "integer"
	}
	return "number"
}

// cdCheck validates raw against s, appending problems at path.
func cdCheck(s *cdSchema, raw json.RawMessage, path string, bad *[]string) {
	if s == nil {
		return
	}
	if len(s.OneOf)+len(s.AnyOf) > 0 {
		for _, alt := range append(append([]*cdSchema{}, s.OneOf...), s.AnyOf...) {
			var p []string
			cdCheck(alt, raw, path, &p)
			if len(p) == 0 {
				return
			}
		}
		*bad = append(*bad, path+": matches none of the alternatives")
		return
	}
	got := cdJSONType(raw)
	if ts := s.types(); len(ts) > 0 {
		ok := false
		for _, w := range ts {
			if w == got || (w == "number" && got == "integer") {
				ok = true
			}
		}
		if got == "null" && s.Nullable {
			ok = true
		}
		if !ok {
			*bad = append(*bad, fmt.Sprintf("%s: %s, documented %v", path, got, ts))
			return
		}
	}
	switch got {
	case "object":
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		for _, r := range s.Required {
			if _, ok := m[r]; !ok {
				*bad = append(*bad, path+"."+r+": required, missing")
			}
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if p, ok := s.Properties[k]; ok {
				cdCheck(p, m[k], path+"."+k, bad)
				continue
			}
			if len(s.AdditionalProperties) > 0 && string(s.AdditionalProperties) != "false" {
				var ap cdSchema
				if json.Unmarshal(s.AdditionalProperties, &ap) == nil {
					cdCheck(&ap, m[k], path+"."+k, bad)
				}
				continue
			}
			if s.Properties != nil {
				*bad = append(*bad, path+"."+k+": printed, not documented")
			}
		}
	case "array":
		var l []json.RawMessage
		if json.Unmarshal(raw, &l) != nil {
			return
		}
		for i, x := range l {
			cdCheck(s.Items, x, fmt.Sprintf("%s[%d]", path, i), bad)
		}
	}
}

// cdResponseSchema is the 200/201 JSON schema of method path.
func cdResponseSchema(t *testing.T, doc map[string]map[string]json.RawMessage, path, method string) *cdSchema {
	t.Helper()
	raw, ok := doc[path][method]
	if !ok {
		t.Fatalf("openapi.json has no %s %s", method, path)
	}
	var op struct {
		Responses map[string]struct {
			Content map[string]struct {
				Schema *cdSchema `json:"schema"`
			} `json:"content"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(raw, &op); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"200", "201"} {
		if r, ok := op.Responses[code]; ok {
			return r.Content["application/json"].Schema
		}
	}
	t.Fatalf("%s %s documents no 200/201", method, path)
	return nil
}

// TestCampaignData_JSONMatchesOpenAPI: each read's --json fits its schema.
func TestCampaignData_JSONMatchesOpenAPI(t *testing.T) {
	b, err := os.ReadFile("../../docs/reference/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	// the checker itself: an undocumented member, a missing required one
	// and a wrong type are each reported
	{
		s := cdResponseSchema(t, spec.Paths, "/v1/tags", "get")
		var bad []string
		cdCheck(s, json.RawMessage(`{"schema_version":1,"kind":"tag.list","count":"1","data":[{"bogus":1}],"extra":true}`), "probe", &bad)
		for _, want := range []string{"probe.extra: printed, not documented", "probe.count: string", "probe.data[0].bogus: printed, not documented"} {
			if !strings.Contains(strings.Join(bad, "\n"), want) {
				t.Fatalf("the schema check misses %q: %v", want, bad)
			}
		}
	}
	c := resRepo(t)
	resHostService(t, c)
	c.CommitAll("first")
	m := cdMemory(t, c, "a row of each noun, body ${var:api_host} ${secret:gopass://acme/db/main}")
	c.MustLore("vars", "set", "api_host", "api.acme.example")
	c.MustLore("tag", "add", "--name=cert_rotation")
	c.MustLore("comment", "add", "--on-table=memories", "--on-id="+m, "--body=agreed")
	task := cdIDOf(t, c.Lore("task", "add", "ship", "--to", "repo", "--commitment", "accepted", "--json"))
	c.MustLore("link", "add", "--entity="+task, "--commit=HEAD")
	c.MustLore("external-source", "add", "--name", "issues", "--kind", "github")
	c.MustLore("meta", "set", m, "owner=alice")
	c.MustLore("resource", "add", "host", "web1", "--set", "provider=hetzner", "--set", "ip=10.0.0.1")
	res := resShow(t, c, "host/web1").ID
	c.MustLore("secrets", "rule", "add", "acme-token", "--regex", "acme_[a-z0-9]{32}", "--yes")
	for _, x := range []struct {
		cli          []string
		path, method string
	}{
		{[]string{"tag", "list"}, "/v1/tags", "get"},
		{[]string{"comment", "list"}, "/v1/comments", "get"},
		{[]string{"comment", "search", "agreed"}, "/v1/comments/search", "post"},
		{[]string{"link", "list"}, "/v1/links", "get"},
		{[]string{"external-source", "list"}, "/v1/external_sources", "get"},
		{[]string{"vars", "list"}, "/v1/vars", "get"},
		{[]string{"vars", "show", "api_host"}, "/v1/vars/{id}", "get"},
		{[]string{"vars", "where", "api_host"}, "/v1/vars/{id}/where", "post"},
		{[]string{"secrets", "list"}, "/v1/secrets", "get"},
		{[]string{"secrets", "show", m}, "/v1/secrets/{id}", "get"},
		{[]string{"secrets", "rules"}, "/v1/scanner/rules", "post"},
		{[]string{"secrets", "allow", "list"}, "/v1/secrets/allows", "get"},
		{[]string{"meta", "keys"}, "/v1/meta/keys", "post"},
		{[]string{"meta", "show", m}, "/v1/metas/{id}", "get"},
		{[]string{"meta", "lint"}, "/v1/meta/lint", "post"},
		{[]string{"resource", "list", "host"}, "/v1/resources", "get"},
		{[]string{"resource", "show", res}, "/v1/resources/{id}", "get"},
		{[]string{"resource", "kinds"}, "/v1/resources/kinds", "post"},
		{[]string{"resource", "report"}, "/v1/resources/report", "post"},
		{[]string{"resource", "stale"}, "/v1/resources/stale", "post"},
		{[]string{"resource", "lint"}, "/v1/resources/lint", "post"},
		{[]string{"resource", "impact", res}, "/v1/resources/{id}/impact", "post"},
		{[]string{"resource", "history", res}, "/v1/resources/{id}/history", "post"},
	} {
		if _, ok := spec.Paths[x.path][x.method]; !ok {
			t.Errorf("%v: openapi.json has no %s %s", x.cli, x.method, x.path)
			continue
		}
		s := cdResponseSchema(t, spec.Paths, x.path, x.method)
		r := c.Lore(append(x.cli, "--json")...)
		if r.Code != 0 {
			t.Errorf("%v --json: %s", x.cli, r)
			continue
		}
		var bad []string
		cdCheck(s, json.RawMessage(strings.TrimSpace(r.Stdout)), strings.Join(x.cli, " "), &bad)
		for _, p := range bad {
			t.Error(p)
		}
	}
}
