//go:build userscen

package userscen

// rest_openapi_test.go — the OpenAPI document `lore api` serves is the
// truth about its routes and its JSON (STORE_SERVER_SPEC 9.6, 9.8):
//
//   - the served /openapi.json is byte-for-byte what `lore spec openapi`
//     writes (a repo with no dynamic kinds), so the published file and the
//     running server never disagree;
//   - its operations are exactly /v1/spec's registry operations, one
//     operationId per operation and the same method and path, plus only the
//     documented collection twins (DELETE/PATCH without {id} of an
//     operation whose id is optional);
//   - every documented route is really served: a probe that the decoder
//     refuses before anything runs (an unknown field or parameter) answers
//     E_INVALID_INPUT naming the probe, never "no route";
//   - the declared success status is the one the server answers, and the
//     declared response schema matches the real JSON of every list route
//     and of real rows' show and add routes: types, null only where the
//     schema says nullable, arrays never null, required members present.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// restOAPI is the part of the served OpenAPI document the checks read.
type restOAPI struct {
	OpenAPI    string                                  `json:"openapi"`
	Paths      map[string]map[string]restOAPIOperation `json:"paths"`
	Components struct {
		Schemas map[string]*restSchema `json:"schemas"`
	} `json:"components"`
	Security []map[string][]string `json:"security"`
}

// restOAPIOperation is one documented operation.
type restOAPIOperation struct {
	OperationID string `json:"operationId"`
	Parameters  []struct {
		Name     string `json:"name"`
		In       string `json:"in"`
		Required bool   `json:"required"`
	} `json:"parameters"`
	RequestBody *struct {
		Required bool `json:"required"`
		Content  map[string]struct {
			Schema *restSchema `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Content map[string]struct {
			Schema *restSchema `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
}

// restSchema is the JSON Schema subset lore writes (surface/docs).
type restSchema struct {
	Ref                  string                 `json:"$ref"`
	Type                 string                 `json:"type"`
	Items                *restSchema            `json:"items"`
	Properties           map[string]*restSchema `json:"properties"`
	Required             []string               `json:"required"`
	AdditionalProperties json.RawMessage        `json:"additionalProperties"`
	AnyOf                []*restSchema          `json:"anyOf"`
	Enum                 []string               `json:"enum"`
}

// restSpecDoc is the part of /v1/spec the checks read.
type restSpecDoc struct {
	LoreVersion string `json:"lore_version"`
	Operations  []struct {
		Name string `json:"name"`
		REST struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"rest"`
		Input []struct {
			Name     string `json:"name"`
			Type     string `json:"type"`
			Required bool   `json:"required"`
		} `json:"input"`
	} `json:"operations"`
	Kinds []json.RawMessage `json:"kinds"`
}

// restDocs fetches and decodes both documents.
func restDocs(t *testing.T, srv *restAPI) (restOAPI, restSpecDoc, string) {
	t.Helper()
	oa := srv.get(t, "/openapi.json")
	if oa.status != http.StatusOK {
		t.Fatalf("/openapi.json: %d %.300s", oa.status, oa.body)
	}
	var doc restOAPI
	if err := json.Unmarshal([]byte(oa.body), &doc); err != nil {
		t.Fatalf("/openapi.json does not decode: %v", err)
	}
	sp := srv.get(t, "/v1/spec")
	if sp.status != http.StatusOK {
		t.Fatalf("/v1/spec: %d %.300s", sp.status, sp.body)
	}
	var spec restSpecDoc
	if err := json.Unmarshal([]byte(sp.body), &spec); err != nil {
		t.Fatalf("/v1/spec does not decode: %v", err)
	}
	return doc, spec, oa.body
}

// restMethods are the HTTP methods an OpenAPI path item may hold.
var restMethods = []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodPut}

// TestRestOpenAPI_ServedEqualsWrittenAndRegistry: the served document is
// the written one, and it lists exactly the registry's routes.
func TestRestOpenAPI_ServedEqualsWrittenAndRegistry(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	doc, spec, raw := restDocs(t, srv)

	// (1) served == `lore spec openapi` from the same binary.
	out := filepath.Join(rw.alice.Home, "spec-out")
	rw.repo.MustLore("spec", "openapi", "--out", out)
	written, err := os.ReadFile(filepath.Join(out, "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(string(written), "\n") != strings.TrimRight(raw, "\n") {
		t.Errorf("served /openapi.json differs from `lore spec openapi` (%d vs %d bytes)", len(raw), len(written))
	}
	if doc.OpenAPI != "3.1.0" || len(doc.Security) != 1 || doc.Security[0]["bearer"] == nil {
		t.Errorf("openapi %q security %v: every operation must need the bearer token", doc.OpenAPI, doc.Security)
	}
	if len(spec.Kinds) != 0 {
		t.Fatalf("precondition: a fresh repo has no kinds, got %d", len(spec.Kinds))
	}

	// (2) the same set of operations and routes.
	type route struct{ method, path string }
	documented := map[route]string{}
	for p, item := range doc.Paths {
		for m, o := range item {
			um := strings.ToUpper(m)
			if !slices.Contains(restMethods, um) {
				t.Errorf("path %s: unknown method key %q", p, m)
				continue
			}
			documented[route{um, p}] = o.OperationID
		}
	}
	registered := map[route]string{}
	names := map[string]bool{}
	for _, o := range spec.Operations {
		if names[o.Name] {
			t.Errorf("/v1/spec lists %s twice", o.Name)
		}
		names[o.Name] = true
		r := route{o.REST.Method, o.REST.Path}
		if prev, dup := registered[r]; dup {
			t.Errorf("%s and %s share the route %s %s", prev, o.Name, r.method, r.path)
		}
		registered[r] = o.Name
		if got, ok := documented[r]; !ok || got != o.Name {
			t.Errorf("%s: /v1/spec route %s %s is documented as %q", o.Name, r.method, r.path, got)
		}
	}
	for r, id := range documented {
		if _, ok := registered[r]; ok {
			continue
		}
		// Only an id-less twin may be extra: the route of an operation
		// whose id is optional with its /{id} segment taken out (any
		// method, the segment anywhere: DELETE /v1/resources, GET
		// /v1/policy, POST /v1/lands/run; INTEG-MIGREST-4). Its
		// operationId is the operation's name plus ".bulk".
		twinOf, isTwin := "", false
		for i := len(r.path); i > len("/v1"); i-- {
			if i < len(r.path) && r.path[i] != '/' {
				continue
			}
			if n, ok := registered[route{r.method, r.path[:i] + "/{id}" + r.path[i:]}]; ok && n+".bulk" == id {
				twinOf, isTwin = n, true
				break
			}
		}
		if !isTwin {
			t.Errorf("documented route %s %s (%s) is not a registered operation", r.method, r.path, id)
			continue
		}
		for _, o := range spec.Operations {
			if o.Name != twinOf {
				continue
			}
			for _, f := range o.Input {
				if f.Name == "id" && f.Required {
					t.Errorf("collection twin %s %s of %s whose id is required", r.method, r.path, id)
				}
			}
		}
	}
	if len(spec.Operations) < 100 {
		t.Errorf("/v1/spec lists only %d operations", len(spec.Operations))
	}
	t.Logf("%d operations, %d documented routes", len(spec.Operations), len(documented))
}

// restProbe is the name no operation has as input: the decoder refuses it
// before the operation runs, so probing a route never writes anything.
const restProbe = "zz_route_probe"

// TestRestOpenAPI_EveryDocumentedRouteIsServed probes every documented
// route; a mux miss answers "no route", a served route refuses the probe.
func TestRestOpenAPI_EveryDocumentedRouteIsServed(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	doc, _, _ := restDocs(t, srv)
	paths := make([]string, 0, len(doc.Paths))
	for p := range doc.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	probed := 0
	for _, p := range paths {
		for m, o := range doc.Paths[p] {
			um := strings.ToUpper(m)
			path := strings.ReplaceAll(p, "{id}", "zz-probe-id")
			req := restReq{method: um, path: path}
			if um == http.MethodGet || um == http.MethodDelete {
				req.path += "?" + restProbe + "=1"
			} else {
				req.body = `{"` + restProbe + `":1}`
			}
			rep := srv.do(t, req)
			probed++
			what := um + " " + p + " (" + o.OperationID + ")"
			if rep.err == nil {
				t.Errorf("%s: probe answered %d without an error: %.200s", what, rep.status, rep.body)
				continue
			}
			if strings.Contains(rep.err.Message, "no route") {
				t.Errorf("%s: documented but not served: %s", what, rep.err.Message)
				continue
			}
			if rep.status != http.StatusBadRequest || rep.err.Code != "E_INVALID_INPUT" || !strings.Contains(rep.err.Message, restProbe) {
				t.Errorf("%s: probe want 400 E_INVALID_INPUT naming %s, got %d %s %q", what, restProbe, rep.status, rep.err.Code, rep.err.Message)
			}
		}
	}
	// Nothing was written by the probes.
	var list struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(rw.repo.MustLore("memory", "list", "--json").Stdout), &list); err != nil || len(list.Data) != 0 {
		t.Errorf("probes wrote %d memories (%v)", len(list.Data), err)
	}
	t.Logf("probed %d routes", probed)
}

// restSchemaCheck validates v against s and returns every mismatch, each
// prefixed by its JSON path. resolve looks up a $ref.
func restSchemaCheck(s *restSchema, v json.RawMessage, at string, resolve func(string) *restSchema) []string {
	if s == nil {
		return nil
	}
	if s.Ref != "" {
		return restSchemaCheck(resolve(s.Ref), v, at, resolve)
	}
	if len(s.AnyOf) > 0 {
		var all []string
		for _, alt := range s.AnyOf {
			p := restSchemaCheck(alt, v, at, resolve)
			if len(p) == 0 {
				return nil
			}
			all = append(all, p...)
		}
		return []string{at + ": matches no anyOf branch (" + strings.Join(all, "; ") + ")"}
	}
	tv := strings.TrimSpace(string(v))
	if s.Type == "null" {
		if tv != "null" {
			return []string{at + ": want null"}
		}
		return nil
	}
	if s.Type == "" {
		return nil // the empty schema: any JSON value
	}
	if tv == "null" {
		return []string{at + ": null, but the schema says " + s.Type + " (not nullable)"}
	}
	kind := restJSONKind(tv)
	want := s.Type
	if want == "integer" {
		if kind != "number" || strings.ContainsAny(tv, ".eE") {
			return []string{at + ": want integer, got " + tv}
		}
		return nil
	}
	if kind != want {
		return []string{at + ": want " + want + ", got " + kind}
	}
	var out []string
	switch want {
	case "object":
		var m map[string]json.RawMessage
		if err := json.Unmarshal(v, &m); err != nil {
			return []string{at + ": " + err.Error()}
		}
		for _, r := range s.Required {
			if _, ok := m[r]; !ok {
				out = append(out, at+"."+r+": required but missing")
			}
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var extra *restSchema
		if len(s.AdditionalProperties) > 0 && string(s.AdditionalProperties) != "false" && string(s.AdditionalProperties) != "true" {
			extra = &restSchema{}
			if err := json.Unmarshal(s.AdditionalProperties, extra); err != nil {
				return []string{at + ": additionalProperties: " + err.Error()}
			}
		}
		for _, k := range keys {
			ps, ok := s.Properties[k]
			if !ok {
				ps = extra
			}
			out = append(out, restSchemaCheck(ps, m[k], at+"."+k, resolve)...)
		}
	case "array":
		var l []json.RawMessage
		if err := json.Unmarshal(v, &l); err != nil {
			return []string{at + ": " + err.Error()}
		}
		for i, x := range l {
			out = append(out, restSchemaCheck(s.Items, x, at+"["+strconv.Itoa(i)+"]", resolve)...)
		}
	case "string":
		if len(s.Enum) > 0 {
			var str string
			if json.Unmarshal(v, &str) == nil && !slices.Contains(s.Enum, str) {
				out = append(out, at+": "+str+" is not in the enum")
			}
		}
	}
	return out
}

// restJSONKind is the JSON type of a trimmed value.
func restJSONKind(tv string) string {
	switch {
	case tv == "":
		return "empty"
	case tv[0] == '{':
		return "object"
	case tv[0] == '[':
		return "array"
	case tv[0] == '"':
		return "string"
	case tv == "true" || tv == "false":
		return "boolean"
	case tv == "null":
		return "null"
	}
	return "number"
}

// restIDRe finds row ids in an add answer.
var restIDRe = regexp.MustCompile(`"id":\s*"([a-z]+_[0-9a-f]{32})"`)

// TestRestOpenAPI_ResponsesMatchDeclaredSchemas seeds rows of several
// kinds, then checks the real JSON of every list route without required
// input, the add answers and the show answers against the schema the
// served document declares for that route and status.
func TestRestOpenAPI_ResponsesMatchDeclaredSchemas(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	doc, spec, _ := restDocs(t, srv)
	resolve := func(ref string) *restSchema {
		return doc.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
	}
	check := func(what, method, path string, rep restReply) {
		t.Helper()
		o, ok := doc.Paths[path][strings.ToLower(method)]
		if !ok {
			t.Errorf("%s: %s %s is not documented", what, method, path)
			return
		}
		resp, ok := o.Responses[strconv.Itoa(rep.status)]
		if !ok {
			t.Errorf("%s: answered %d, which the document does not declare: %.300s", what, rep.status, rep.body)
			return
		}
		media, ok := resp.Content["application/json"]
		if !ok {
			t.Errorf("%s: status %d has no JSON schema", what, rep.status)
			return
		}
		for _, p := range restSchemaCheck(media.Schema, json.RawMessage(rep.body), "$", resolve) {
			t.Errorf("%s (%d): %s", what, rep.status, p)
		}
	}

	// Seed one row of several kinds over REST, checking each add answer.
	adds := []struct{ path, body string }{
		{"/v1/memories", `{"body":"a memory for the schema walk"}`},
		{"/v1/tasks", `{"title":"a task for the schema walk","to":"repo","commitment":"accepted"}`},
		{"/v1/decisions", `{"title":"use the schema walk","body":"because"}`},
		{"/v1/hotfixes", `{"title":"a hotfix for the walk","body":"x"}`},
		{"/v1/incidents", `{"title":"an incident for the walk","body":"x"}`},
		{"/v1/playbooks", `{"title":"a playbook for the walk","body":"x"}`},
	}
	var shows [][2]string // {path template, id}
	for _, a := range adds {
		rep := srv.post(t, a.path, a.body)
		if rep.status != http.StatusCreated {
			t.Errorf("POST %s: %d %.300s", a.path, rep.status, rep.body)
			continue
		}
		check("POST "+a.path, http.MethodPost, a.path, rep)
		m := restIDRe.FindStringSubmatch(rep.body)
		if m == nil {
			t.Errorf("POST %s: no id in %s", a.path, rep.body)
			continue
		}
		shows = append(shows, [2]string{a.path + "/{id}", m[1]})
	}
	for _, s := range shows {
		path := strings.ReplaceAll(s[0], "{id}", s[1])
		rep := srv.get(t, path)
		if rep.status != http.StatusOK {
			t.Errorf("GET %s: %d %.300s", path, rep.status, rep.body)
			continue
		}
		check("GET "+path, http.MethodGet, s[0], rep)
	}

	// Every GET without {id} and without required input.
	walked := 0
	for _, o := range spec.Operations {
		if o.REST.Method != http.MethodGet || strings.Contains(o.REST.Path, "{") {
			continue
		}
		required := false
		for _, f := range o.Input {
			required = required || f.Required
		}
		if required {
			continue
		}
		rep := srv.get(t, o.REST.Path)
		walked++
		if rep.status == http.StatusInternalServerError {
			t.Errorf("GET %s (%s): 500 %.300s", o.REST.Path, o.Name, rep.body)
		}
		check("GET "+o.REST.Path+" ("+o.Name+")", http.MethodGet, o.REST.Path, rep)
	}
	t.Logf("walked %d list routes, %d adds, %d shows", walked, len(adds), len(shows))
}
