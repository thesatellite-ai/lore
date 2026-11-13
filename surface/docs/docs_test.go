package docs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

type addIn struct {
	Body string   `arg:"0" json:"body" desc:"the memory text"`
	Tags []string `flag:"tag" json:"tags,omitempty" desc:"a tag"`
	Pin  bool     `flag:"pin" json:"pin" desc:"pin it"`
}

type idIn struct {
	ID    string `arg:"0" json:"id" desc:"the row"`
	Limit uint   `flag:"limit" json:"limit,omitempty"`
}

type out struct {
	ID    string `json:"id"`
	Items []struct {
		N int `json:"n"`
	} `json:"items,omitempty"`
}

func noop[In any](context.Context, In) (out, error) { return out{}, nil }

// registry builds the fake registry; summary changes memory.add's summary
// (the stale break-verify) and reverse defines the entries in reverse order.
func registry(t *testing.T, summary string, reverse bool) *op.Registry {
	t.Helper()
	entries := []op.Entry{
		op.Define(op.Spec{Name: "memory.add", Summary: summary, Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: []errs.Code{errs.EmptyBody, errs.SecretDetected, errs.Stale}}, noop[addIn]),
		op.Define(op.Spec{Name: "memory.show", Summary: "Show a memory", Permission: op.PermRead, Access: op.AccessRead,
			Errors: []errs.Code{errs.NotFound}}, noop[idIn]),
		op.Define(op.Spec{Name: "trash.purge", Summary: "Purge | forever", Permission: op.PermAdmin, Access: op.AccessWrite,
			Confirm: op.ConfirmRule{Always: true}}, noop[idIn]),
		op.Define(op.Spec{Name: "merge.driver", Summary: "plumbing", Permission: op.PermWrite, Access: op.AccessWrite, Hidden: true}, noop[addIn]),
	}
	if reverse {
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
	}
	reg, err := op.NewRegistry(entries...)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// The parts of the document the structure test reads.
type schemaDoc struct {
	Type                 string               `json:"type"`
	Ref                  string               `json:"$ref"`
	Properties           map[string]schemaDoc `json:"properties"`
	Required             []string             `json:"required"`
	AdditionalProperties json.RawMessage      `json:"additionalProperties"`
	Items                *schemaDoc           `json:"items"`
	Format               string               `json:"format"`
	Enum                 []string             `json:"enum"`
}

type opDoc struct {
	OperationID string `json:"operationId"`
	Parameters  []struct {
		Name     string `json:"name"`
		In       string `json:"in"`
		Required bool   `json:"required"`
	} `json:"parameters"`
	RequestBody *struct {
		Content map[string]struct {
			Schema schemaDoc `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Description string `json:"description"`
		Content     map[string]struct {
			Schema schemaDoc `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
}

type openapiDoc struct {
	OpenAPI    string                      `json:"openapi"`
	Security   []map[string][]string       `json:"security"`
	Paths      map[string]map[string]opDoc `json:"paths"`
	Components struct {
		Schemas         map[string]schemaDoc `json:"schemas"`
		SecuritySchemes map[string]struct {
			Type   string `json:"type"`
			Scheme string `json:"scheme"`
		} `json:"securitySchemes"`
	} `json:"components"`
}

func parse(t *testing.T, b []byte) openapiDoc {
	t.Helper()
	var d openapiDoc
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("openapi is not JSON: %v", err)
	}
	return d
}

// TestOpenAPIStructure: the document parses, has one path+method per
// non-hidden operation, requires the bearer token everywhere, describes
// inputs as closed objects or query/path parameters, adds "yes" and 428 to
// confirming operations, and maps every error code to its class status.
func TestOpenAPIStructure(t *testing.T) {
	t.Parallel()
	reg := registry(t, "Add a memory", false)
	b, err := OpenAPI(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := parse(t, b)
	if d.OpenAPI != OpenAPIVersion || len(d.Security) != 1 || d.Components.SecuritySchemes[SecurityScheme].Scheme != SecurityScheme {
		t.Fatalf("header %+v %+v", d.OpenAPI, d.Security)
	}
	if _, ok := d.Components.Schemas[ErrorSchema]; !ok {
		t.Fatal("no Error schema")
	}
	count := 0
	for _, e := range reg.Entries() {
		rt, ok := op.RouteOf(e.Spec(), e.InputFields())
		if !ok {
			for p := range d.Paths {
				if strings.Contains(p, "merge") {
					t.Errorf("hidden op documented at %s", p)
				}
			}
			continue
		}
		count++
		o, ok := d.Paths[rt.Path][strings.ToLower(rt.Method)]
		if !ok || o.OperationID != e.Spec().Name {
			t.Fatalf("%s: no %s %s", e.Spec().Name, rt.Method, rt.Path)
		}
		ok200 := o.Responses[strconv.Itoa(rt.Status)]
		if ok200.Content["application/json"].Schema.Type != "object" {
			t.Errorf("%s: success schema %+v", e.Spec().Name, ok200)
		}
		for _, status := range []string{"400", "401", "500"} {
			if _, ok := o.Responses[status]; !ok {
				t.Errorf("%s: no %s response", e.Spec().Name, status)
			}
		}
		if !strings.Contains(o.Responses["500"].Description, string(errs.Internal)) || strings.Contains(o.Responses["500"].Description, ",") {
			t.Errorf("%s: 500 is %q, want only E_INTERNAL", e.Spec().Name, o.Responses["500"].Description)
		}
		for _, c := range e.Spec().Errors {
			r, ok := o.Responses[strconv.Itoa(errs.DefOf(c).HTTP)]
			if !ok || !strings.Contains(r.Description, string(c)) {
				t.Errorf("%s: %s not under its class status", e.Spec().Name, c)
			}
		}
	}
	if count != 3 {
		t.Fatalf("documented %d ops, want 3", count)
	}
	add := d.Paths["/v1/memories"]["post"]
	body := add.RequestBody.Content["application/json"].Schema
	if string(body.AdditionalProperties) != "false" || body.Required[0] != "body" || body.Properties["tags"].Items.Type != "string" {
		t.Errorf("add body %+v", body)
	}
	if _, ok := body.Properties[YesField]; ok {
		t.Error("yes on a non-confirming op")
	}
	if add.Responses["201"].Description != "Created" {
		t.Errorf("add status %+v", add.Responses)
	}
	show := d.Paths["/v1/memories/{id}"]["get"]
	if show.RequestBody != nil || len(show.Parameters) != 2 || show.Parameters[0].In != "path" || show.Parameters[1].In != "query" {
		t.Errorf("show params %+v", show.Parameters)
	}
	purge := d.Paths["/v1/trashes/{id}/purge"]["post"]
	if _, ok := purge.Responses["428"]; !ok {
		t.Error("confirming op lacks 428")
	}
	if _, ok := purge.RequestBody.Content["application/json"].Schema.Properties[YesField]; !ok {
		t.Error("confirming op lacks the yes field")
	}
}

// TestOpenAPIKinds: kinds become component schemas with typed fields and
// POST|GET /v1/resources/<kind> paths.
func TestOpenAPIKinds(t *testing.T) {
	t.Parallel()
	server := schema.TableDef{Name: "server", Fields: []schema.FieldDef{
		{Name: "ip", Kind: value.KindIP, Required: true},
		{Name: "region", Kind: value.KindText, Values: []string{"eu", "us"}},
		{Name: "cores", Kind: value.KindNumber},
		{Name: "tags", Kind: value.KindList, Elem: value.KindText},
	}}
	b, err := OpenAPI(registry(t, "Add", false), []schema.TableDef{server})
	if err != nil {
		t.Fatal(err)
	}
	k := parse(t, b).Components.Schemas[KindSchemaPrefix+"server"]
	if k.Properties["ip"].Type != "string" || k.Required[0] != "ip" || k.Properties["cores"].Type != "number" ||
		len(k.Properties["region"].Enum) != 2 || k.Properties["tags"].Items.Type != "string" {
		t.Fatalf("kind schema %+v", k)
	}
	// The per-kind routes (kindroutes.go): POST with the kind schema as
	// "fields", GET with where.
	p := parse(t, b).Paths["/v1/resources/server"]
	if p["get"].OperationID == "" || p["post"].RequestBody == nil || p["post"].RequestBody.Content["application/json"].Schema.Properties["fields"].Ref != refSchemas+KindSchemaPrefix+"server" {
		t.Fatalf("kind paths %+v", p)
	}
}

// TestDeterministic: two runs, and registries built in different orders,
// give byte-equal output.
func TestDeterministic(t *testing.T) {
	t.Parallel()
	a, b := registry(t, "Add", false), registry(t, "Add", true)
	oa, _ := OpenAPI(a, nil)
	oa2, _ := OpenAPI(a, nil)
	ob, _ := OpenAPI(b, nil)
	if string(oa) != string(oa2) || string(oa) != string(ob) {
		t.Fatal("openapi not deterministic")
	}
	e1, e2 := ErrorsMarkdown(), ErrorsMarkdown()
	if CommandsMarkdown(a) != CommandsMarkdown(b) || e1 != e2 {
		t.Fatal("markdown not deterministic")
	}
}

// TestMarkdown: the command table lists non-hidden ops with usage, flags,
// route and errors, escaping pipes; the error table has every code.
func TestMarkdown(t *testing.T) {
	t.Parallel()
	cmds := CommandsMarkdown(registry(t, "Add a memory", false))
	for _, want := range []string{
		"| `lore memory add --body <body>` | Add a memory | `--tag` `--pin` | `POST /v1/memories` | write | `E_EMPTY_BODY` `E_SECRET_DETECTED` `E_STALE` |",
		"| `lore trash purge --id <id>` | Purge \\| forever | `--limit` | `POST /v1/trashes/{id}/purge` | admin |  |",
	} {
		if !strings.Contains(cmds, want) {
			t.Errorf("commands lack\n%s\nin\n%s", want, cmds)
		}
	}
	if strings.Contains(cmds, "merge") {
		t.Error("hidden op listed")
	}
	errTable := ErrorsMarkdown()
	for _, d := range errs.All() {
		row := "| `" + string(d.Code) + "` | " + strconv.Itoa(d.Exit) + " | `" + string(d.Class) + "` | " + strconv.Itoa(d.HTTP) + " | "
		if !strings.Contains(errTable, row) {
			t.Errorf("error table lacks %s", row)
		}
	}
}

// recTB records failures instead of failing the real test.
type recTB struct {
	testing.TB
	failures []string
}

func (r *recTB) Errorf(format string, args ...any) {
	r.failures = append(r.failures, format)
}

// TestCheckStale: Update writes the blocks and the JSON, keeps hand-written
// text, CheckStale passes on fresh files and fails once a Summary changes
// (break-verify of the drift guard), and passes again after Update.
func TestCheckStale(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	md := filepath.Join(dir, "CLI_REFERENCE.md")
	js := filepath.Join(dir, "openapi.json")
	hand := "# Reference\n\nhand text\n\n<!-- lore:spec:begin commands -->\nold\n<!-- lore:spec:end commands -->\n\nmiddle\n\n<!-- lore:spec:begin errors -->\n<!-- lore:spec:end errors -->\ntail\n"
	if err := os.WriteFile(md, []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := registry(t, "Add a memory", false)
	rec := &recTB{TB: t}
	CheckStale(rec, reg, md, js)
	if len(rec.failures) != 2 {
		t.Fatalf("stale/missing files not reported: %v", rec.failures)
	}
	if err := Update(reg, md, js); err != nil {
		t.Fatal(err)
	}
	rec = &recTB{TB: t}
	CheckStale(rec, reg, md, js)
	if len(rec.failures) != 0 {
		t.Fatalf("fresh files reported: %v", rec.failures)
	}
	got, _ := os.ReadFile(filepath.Clean(md))
	for _, keep := range []string{"# Reference\n\nhand text\n\n", "\n\nmiddle\n\n", "tail\n", "| `lore memory add --body <body>` | Add a memory", "| `E_INTERNAL` | 1 | `generic` | 500 |"} {
		if !strings.Contains(string(got), keep) {
			t.Errorf("updated markdown lacks %q", keep)
		}
	}
	// Windows has no POSIX mode bits (os.Chmod only sets read-only)
	if st, _ := os.Stat(md); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %v", st.Mode().Perm())
	}
	changed := registry(t, "Add one memory", false)
	rec = &recTB{TB: t}
	CheckStale(rec, changed, md, js)
	if len(rec.failures) != 2 {
		t.Fatalf("changed Summary not caught: %v", rec.failures)
	}
	if err := Update(changed, md, js); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Clean(md))
	if err := Update(changed, md); err != nil {
		t.Fatal(err)
	}
	twice, _ := os.ReadFile(filepath.Clean(md))
	if string(again) != string(twice) {
		t.Fatal("Update not idempotent")
	}
}

// TestStaleCheckError: `lore spec docs --check` is Stale + CheckError:
// fresh files give no drift and a nil error; a changed Summary gives one
// Drift per file (line and both versions) and an E_CONFIG_INVALID naming
// every stale path and the regenerate command; a missing file is an
// error, never "current".
func TestStaleCheckError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	md := filepath.Join(dir, "cli-commands.md")
	if err := os.WriteFile(md, []byte("<!-- lore:spec:begin commands -->\n<!-- lore:spec:end commands -->\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := registry(t, "Add a memory", false)
	if err := Update(reg, md); err != nil {
		t.Fatal(err)
	}
	drift, err := Stale(reg, md)
	if err != nil || len(drift) != 0 || CheckError(drift) != nil {
		t.Fatalf("fresh: drift %v err %v", drift, err)
	}
	drift, err = Stale(registry(t, "Add one memory", false), md)
	if err != nil || len(drift) != 1 || drift[0].Line == 0 || drift[0].Committed == drift[0].Generated {
		t.Fatalf("changed: drift %+v err %v", drift, err)
	}
	cerr := CheckError(drift)
	if !errs.Has(cerr, errs.ConfigInvalid) || !strings.Contains(cerr.Error(), md) {
		t.Fatalf("CheckError = %v", cerr)
	}
	if _, err := Stale(reg, filepath.Join(dir, "missing.md")); err == nil {
		t.Fatal("a missing file read as current")
	}
}

// TestRenderRefuses: unknown extension, no block, unknown block, missing
// end marker.
func TestRenderRefuses(t *testing.T) {
	t.Parallel()
	reg := registry(t, "Add", false)
	cases := map[string]string{
		"x.txt": "",
		"a.md":  "no blocks",
		"b.md":  "<!-- lore:spec:begin nope -->\n<!-- lore:spec:end nope -->",
		"c.md":  "<!-- lore:spec:begin errors -->\n",
		"d.md":  "<!-- lore:spec:begin errors",
	}
	for path, src := range cases {
		if _, err := Render(reg, path, []byte(src)); !errs.Has(err, errs.ConfigInvalid) {
			t.Errorf("%s: %v", path, err)
		}
	}
	if err := Update(reg, filepath.Join(t.TempDir(), "missing.md")); !errs.Has(err, errs.NotFound) {
		t.Errorf("missing md: %v", err)
	}
}

type bulkIn struct {
	ID    string   `arg:"0" json:"id,omitempty" desc:"one row; omit for the bulk form"`
	Kind  string   `flag:"kind" json:"kind,omitempty" desc:"every row of this kind"`
	Where []string `flag:"where" json:"where,omitempty" desc:"filter"`
}

// TestOpenAPIBulkTwins: an operation whose id is optional also gets its
// id-less twin (the route without its /{id} segment, the routes
// surface/rest serves): DELETE|PATCH /v1/<noun> for the bulk form
// (P2-RES-1), and any other verb too (GET /v1/hosts for a show, POST
// /v1/hosts/run; INTEG-MIGREST-4), each with a unique operationId and the
// id as an optional input; an op whose id is required gets none, and a
// twin never shadows a registry route (the list keeps GET /v1/gadgets).
func TestOpenAPIBulkTwins(t *testing.T) {
	t.Parallel()
	reg, err := op.NewRegistry(
		op.Define(op.Spec{Name: "host.delete", Summary: "Delete hosts", Permission: op.PermWrite, Access: op.AccessWrite}, noop[bulkIn]),
		op.Define(op.Spec{Name: "host.edit", Summary: "Edit hosts", Permission: op.PermWrite, Access: op.AccessWrite}, noop[bulkIn]),
		op.Define(op.Spec{Name: "host.show", Summary: "Show a host", Permission: op.PermRead, Access: op.AccessRead}, noop[bulkIn]),
		op.Define(op.Spec{Name: "host.run", Summary: "Run a host", Permission: op.PermWrite, Access: op.AccessWrite}, noop[bulkIn]),
		op.Define(op.Spec{Name: "gadget.list", Summary: "List gadgets", Permission: op.PermRead, Access: op.AccessRead}, noop[addIn]),
		op.Define(op.Spec{Name: "gadget.show", Summary: "Show a gadget", Permission: op.PermRead, Access: op.AccessRead}, noop[bulkIn]),
		op.Define(op.Spec{Name: "memory.delete", Summary: "Delete a memory", Permission: op.PermWrite, Access: op.AccessWrite}, noop[idIn]),
	)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenAPI(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := parse(t, b)
	del, ok := d.Paths["/v1/hosts"]["delete"]
	if !ok || del.OperationID != "host.delete"+BulkOperationSuffix {
		t.Fatalf("no DELETE twin: %+v", d.Paths["/v1/hosts"])
	}
	for _, p := range del.Parameters {
		if p.Name == IDParam && (p.In != inQuery || p.Required) {
			t.Errorf("twin id param %+v, want optional query", p)
		}
	}
	patch, ok := d.Paths["/v1/hosts"]["patch"]
	if !ok || patch.OperationID != "host.edit"+BulkOperationSuffix || patch.RequestBody == nil {
		t.Fatalf("no PATCH twin: %+v", d.Paths["/v1/hosts"])
	}
	if _, ok := patch.RequestBody.Content[mediaJSON].Schema.Properties[IDParam]; !ok {
		t.Error("PATCH twin body lacks the optional id")
	}
	if show, ok := d.Paths["/v1/hosts"]["get"]; !ok || show.OperationID != "host.show"+BulkOperationSuffix || len(d.Paths["/v1/hosts"]) != 3 {
		t.Errorf("/v1/hosts methods %v, want delete, patch and the show twin", d.Paths["/v1/hosts"])
	}
	if run, ok := d.Paths["/v1/hosts/run"]["post"]; !ok || run.OperationID != "host.run"+BulkOperationSuffix {
		t.Errorf("no POST /v1/hosts/run twin of /v1/hosts/{id}/run: %v", d.Paths["/v1/hosts/run"])
	}
	if get := d.Paths["/v1/gadgets"]["get"]; get.OperationID != "gadget.list" {
		t.Errorf("the show twin shadowed the list: GET /v1/gadgets is %q", get.OperationID)
	}
	if _, ok := d.Paths["/v1/hosts/{id}"]["delete"]; !ok {
		t.Error("the /{id} route itself is gone")
	}
	if _, ok := d.Paths["/v1/memories"]; ok {
		t.Error("a required id got a twin")
	}
}
