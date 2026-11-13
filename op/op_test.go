package op

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
)

type addIn struct {
	Body    string              `arg:"0" json:"body" desc:"the memory text" ui:"widget=markdown"`
	Title   string              `flag:"title" json:"title,omitempty" ui:"label=Title,in_list=true"`
	Tags    []string            `flag:"tag" json:"tags,omitempty"`
	Meta    map[string]string   `flag:"meta" json:"meta,omitempty"`
	Limit   int                 `flag:"limit" json:"limit,omitempty"`
	Wait    time.Duration       `flag:"wait" json:"wait,omitempty"`
	Project ids.ID[ids.Project] `flag:"project" json:"project,omitempty"`
	Force   bool                `flag:"force" json:"force"`
}

type addOut struct {
	ID    string `json:"id"`
	Store string `json:"store"`
	Items []struct {
		N int `json:"n"`
	} `json:"items,omitempty"`
}

func addOp() *Op[addIn, addOut] {
	return Define(Spec{
		Name: "memory.add", Summary: "Add a memory", Permission: PermWrite, Access: AccessWrite,
		Errors:  []errs.Code{errs.EmptyBody, errs.Stale},
		Confirm: ConfirmRule{When: "force"},
	}, func(_ context.Context, in addIn) (addOut, error) {
		return addOut{ID: "mem_x", Store: in.Title + "|" + strings.Join(in.Tags, ",")}, nil
	})
}

func TestFieldsFromTags(t *testing.T) {
	t.Parallel()
	in := addOp().InputFields()
	want := map[string]FieldType{"body": TypeString, "title": TypeString, "tags": TypeStrings, "meta": TypeStringMap, "limit": TypeInt, "wait": TypeDuration, "project": TypeText, "force": TypeBool}
	if len(in) != len(want) {
		t.Fatalf("fields %d", len(in))
	}
	for _, f := range in {
		if want[f.Name] != f.Type {
			t.Errorf("%s: type %s want %s", f.Name, f.Type, want[f.Name])
		}
	}
	body := in[0]
	if body.Arg != 0 || !body.Required || body.UI.Widget != schema.WidgetMarkdown || body.Desc != "the memory text" {
		t.Errorf("body %+v", body)
	}
	if in[1].Required || in[1].UI.Label != "Title" || in[1].UI.InList != schema.InListYes || in[1].Flag != "title" {
		t.Errorf("title %+v", in[1])
	}
	if in[7].Required {
		t.Error("bool field marked required")
	}
	out := addOp().OutputFields()
	if out[2].Type != TypeList || len(out[2].Fields) != 1 || out[2].Fields[0].Type != TypeInt {
		t.Errorf("nested output %+v", out[2])
	}
}

func TestDecodeArgsAndInvoke(t *testing.T) {
	t.Parallel()
	o := addOp()
	in, err := o.Decode(ArgsDecoder{
		Args:  []string{"Retry on 502"},
		Flags: map[string][]string{"title": {"T"}, "tag": {"a", "b"}, "meta": {"jira=HUB-1"}, "limit": {"5"}, "wait": {"1h30m"}, "force": {"true"}, "project": {"prj_01936f2a7c4b7e1d8a3f5b6c7d8e9f00"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := in.Bool("force"); !ok || !f {
		t.Fatal("Bool(force)")
	}
	texts := map[string]string{}
	for _, tv := range in.Texts() {
		texts[tv.Field+"="+tv.Text] = tv.Text
	}
	for _, k := range []string{"body=Retry on 502", "title=T", "tags=a", "tags=b", "meta=jira", "meta.jira=HUB-1"} {
		if _, ok := texts[k]; !ok {
			t.Errorf("Texts lacks %s: %v", k, texts)
		}
	}
	res, err := o.Invoke(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := res.JSON()
	if err != nil || string(b) != `{"id":"mem_x","store":"T|a,b"}` {
		t.Fatalf("result %s %v", b, err)
	}
}

func TestDecodeRefuses(t *testing.T) {
	t.Parallel()
	o := addOp()
	cases := map[string]Decoder{
		"missing required": ArgsDecoder{},
		"unknown flag":     ArgsDecoder{Args: []string{"x"}, Flags: map[string][]string{"colour": {"red"}}},
		"too many args":    ArgsDecoder{Args: []string{"x", "y"}},
		"bad int":          ArgsDecoder{Args: []string{"x"}, Flags: map[string][]string{"limit": {"many"}}},
		"two values":       ArgsDecoder{Args: []string{"x"}, Flags: map[string][]string{"title": {"a", "b"}}},
		"bad map":          ArgsDecoder{Args: []string{"x"}, Flags: map[string][]string{"meta": {"novalue"}}},
		"bad id":           ArgsDecoder{Args: []string{"x"}, Flags: map[string][]string{"project": {"mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00"}}},
		"json unknown":     JSONDecoder{Body: []byte(`{"body":"x","colour":"red"}`)},
		"json type":        JSONDecoder{Body: []byte(`{"body":5}`)},
		"json trailing":    JSONDecoder{Body: []byte(`{"body":"x"} {}`)},
		"json missing":     JSONDecoder{Body: []byte(`{}`)},
	}
	for name, d := range cases {
		if _, err := o.Decode(d); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	in, err := o.Decode(JSONDecoder{Body: []byte(`{"body":"x","tags":["a"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	other := Define(Spec{Name: "memory.list", Summary: "s", Permission: PermRead, Access: AccessRead},
		func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })
	if _, err := other.Invoke(context.Background(), in); !errs.Has(err, errs.Internal) {
		t.Fatal("input of another op accepted")
	}
}

func TestDefinePanicsOnBadTags(t *testing.T) {
	t.Parallel()
	run := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", name)
			}
		}()
		f()
	}
	type noJSON struct{ A string }
	type badUI struct {
		A string `json:"a" ui:"widget=slider"`
	}
	type gap struct {
		A string `arg:"1" json:"a"`
	}
	// U-4: a positional's derived flag (its json name) may not clash with
	// another field's flag.
	type both struct {
		A string `arg:"0" json:"a"`
		B string `flag:"a" json:"b"`
	}
	type nested struct {
		A struct{} `json:"a"`
	}
	type badFlag struct {
		A string `flag:"A_b" json:"a"`
	}
	noop := func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }
	run("bad name", func() { Define(Spec{Name: "memory"}, noop) })
	run("no json", func() {
		Define(Spec{Name: "a.b"}, func(context.Context, noJSON) (struct{}, error) { return struct{}{}, nil })
	})
	run("bad ui", func() {
		Define(Spec{Name: "a.b"}, func(context.Context, badUI) (struct{}, error) { return struct{}{}, nil })
	})
	run("arg gap", func() {
		Define(Spec{Name: "a.b"}, func(context.Context, gap) (struct{}, error) { return struct{}{}, nil })
	})
	run("positional flag clash", func() {
		Define(Spec{Name: "a.b"}, func(context.Context, both) (struct{}, error) { return struct{}{}, nil })
	})
	run("nested input", func() {
		Define(Spec{Name: "a.b"}, func(context.Context, nested) (struct{}, error) { return struct{}{}, nil })
	})
	run("bad flag", func() {
		Define(Spec{Name: "a.b"}, func(context.Context, badFlag) (struct{}, error) { return struct{}{}, nil })
	})
	run("not a struct", func() {
		Define(Spec{Name: "a.b"}, func(context.Context, string) (struct{}, error) { return struct{}{}, nil })
	})
}

func TestRoutesAndCLI(t *testing.T) {
	t.Parallel()
	idIn := []FieldInfo{{Name: "id"}}
	cases := []struct {
		spec   Spec
		in     []FieldInfo
		method string
		path   string
		status int
	}{
		{Spec{Name: "memory.add"}, nil, http.MethodPost, "/v1/memories", 201},
		{Spec{Name: "memory.list"}, nil, http.MethodGet, "/v1/memories", 200},
		{Spec{Name: "memory.show"}, idIn, http.MethodGet, "/v1/memories/{id}", 200},
		{Spec{Name: "memory.edit"}, idIn, http.MethodPatch, "/v1/memories/{id}", 200},
		{Spec{Name: "memory.delete"}, idIn, http.MethodDelete, "/v1/memories/{id}", 200},
		{Spec{Name: "task.claim"}, idIn, http.MethodPost, "/v1/tasks/{id}/claim", 200},
		{Spec{Name: "policy.set"}, nil, http.MethodPost, "/v1/policies/set", 200},
		{Spec{Name: "policy.set", RESTNoun: "policy"}, nil, http.MethodPost, "/v1/policy/set", 200},
		{Spec{Name: "secrets.rule.add"}, nil, http.MethodPost, "/v1/secrets/rules", 201},
		{Spec{Name: "vars.set"}, nil, http.MethodPost, "/v1/vars/set", 200},
		{Spec{Name: "box.add"}, nil, http.MethodPost, "/v1/boxes", 201},
	}
	for _, c := range cases {
		r, ok := RouteOf(c.spec, c.in)
		if !ok || r.Method != c.method || r.Path != c.path || r.Status != c.status {
			t.Errorf("%s: %+v want %s %s %d", c.spec.Name, r, c.method, c.path, c.status)
		}
	}
	if _, ok := RouteOf(Spec{Name: "merge.driver", Hidden: true}, nil); ok {
		t.Error("hidden op got a route")
	}
	if got := strings.Join(CLIPath("repo.set_url"), " "); got != "repo set-url" {
		t.Errorf("CLIPath = %q", got)
	}
	if Plural("key") != "keys" || Plural("entry") != "entries" || Plural("class") != "classes" {
		t.Error("Plural")
	}
}

func TestRegistry(t *testing.T) {
	t.Parallel()
	r, err := NewRegistry(addOp())
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := r.Lookup("memory.add"); !ok || e.Spec().Name != "memory.add" || len(r.Entries()) != 1 {
		t.Fatal("lookup")
	}
	noop := func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }
	bad := map[string][]Entry{
		"duplicate":     {addOp(), addOp()},
		"unknown code":  {Define(Spec{Name: "a.list", Summary: "s", Permission: PermRead, Access: AccessRead, Errors: []errs.Code{"E_NOPE"}}, noop)},
		"bad perm":      {Define(Spec{Name: "a.list", Summary: "s", Permission: "root", Access: AccessRead}, noop)},
		"write as read": {Define(Spec{Name: "a.add", Summary: "s", Permission: PermRead, Access: AccessWrite}, noop)},
		"no summary":    {Define(Spec{Name: "a.list", Permission: PermRead, Access: AccessRead}, noop)},
		"confirm when":  {Define(Spec{Name: "a.list", Summary: "s", Permission: PermRead, Access: AccessRead, Confirm: ConfirmRule{When: "force"}}, noop)},
		"route clash": {
			Define(Spec{Name: "policy.set", Summary: "s", Permission: PermWrite, Access: AccessWrite}, noop),
			Define(Spec{Name: "policies.set", Summary: "s", Permission: PermWrite, Access: AccessWrite}, noop),
		},
	}
	for name, entries := range bad {
		if _, err := NewRegistry(entries...); !errs.Has(err, errs.ConfigInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
