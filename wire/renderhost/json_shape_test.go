package renderhost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/op"
	contextop "github.com/thesatellite-ai/lore/ops/context"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
)

// TestRenderJSONMatchesDeclaredShape: every member of the JSON this host
// writes for render.run (--report --json, --report --scopes --json,
// --trend --json) and context.fetch (the picks, the topic index) is a
// declared output field of the operation, which is what
// docs/reference/openapi.json is built from; and the bytes decode into
// the operation's output without error. The host's structs are filled
// by reflection (every field non-zero, every list one item), so a member
// added to them later fails here until it is declared.
func TestRenderJSONMatchesDeclaredShape(t *testing.T) {
	ctx := context.Background()
	rep := filled[renderReport](t)
	reportJSON, err := rep.reportJSON()
	if err != nil {
		t.Fatal(err)
	}
	scopeJSON, err := filled[scopeReport](t).jsonBody()
	if err != nil {
		t.Fatal(err)
	}
	trendJSON, err := json.Marshal(filled[trendOut](t))
	if err != nil {
		t.Fatal(err)
	}
	renderFields := outputFields(t, opsrender.Ops(nil), opsrender.OpRun)
	// "parts" is described on ScopeChain: the field model has no integer list.
	undeclared := []string{"data.chains[].parts"}
	for name, raw := range map[string][]byte{"--report": reportJSON, "--report --scopes": scopeJSON, "--trend": trendJSON} {
		checkDeclared(t, "render "+name, raw, renderFields, undeclared)
		out, rerr := opsrender.Run(ctx, answerHost{a: opsrender.Answer{Text: "x\n", Data: raw}}, opsrender.RunIn{Report: true})
		if rerr != nil {
			t.Fatalf("render %s: the host JSON decodes into RunOut: %v", name, rerr)
		}
		var want bytes.Buffer
		if cerr := json.Compact(&want, raw); cerr != nil {
			t.Fatal(cerr)
		}
		if b, merr := json.Marshal(out); merr != nil || string(b) != want.String() {
			t.Errorf("render %s: the wire bytes are the host's own: %s", name, b)
		}
	}
	ctxFields := outputFields(t, contextop.Ops(nil), contextop.OpFetch)
	picks, err := json.Marshal([]contextPick{filled[contextPick](t)})
	if err != nil {
		t.Fatal(err)
	}
	topic := topicLine{Name: "billing", Counts: map[string]int{}}
	for _, k := range indexKinds {
		topic.Counts[k.label] = 1
	}
	topics, err := json.Marshal([]topicLine{topic})
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{"picks": picks, "topics": topics} {
		wrapped := []byte(`{"data":` + string(raw) + `}`)
		checkDeclared(t, "context "+name, wrapped, ctxFields, nil)
		out, ferr := contextop.Fetch(ctx, fetchHost{a: contextop.Answer{Text: "x\n", Data: raw}}, contextop.FetchIn{Topics: true}, strings.NewReader(""))
		if ferr != nil || len(out.Data) != 1 {
			t.Fatalf("context %s: decodes into FetchOut.Data: %v %+v", name, ferr, out.Data)
		}
		if b, merr := json.Marshal(out); merr != nil || string(b) != string(wrapped) {
			t.Errorf("context %s: the wire bytes are {\"data\": host's own}: %s", name, b)
		}
	}
}

type answerHost struct{ a opsrender.Answer }

func (h answerHost) Render(context.Context, opsrender.RunIn) (opsrender.Answer, error) {
	return h.a, nil
}

type fetchHost struct{ a contextop.Answer }

func (h fetchHost) Fetch(context.Context, contextop.FetchIn, io.Reader) (contextop.Answer, error) {
	return h.a, nil
}

// outputFields is the declared output of the named operation.
func outputFields(t *testing.T, entries []op.Entry, name string) []op.FieldInfo {
	t.Helper()
	for _, e := range entries {
		if e.Spec().Name == name {
			return e.OutputFields()
		}
	}
	t.Fatalf("no operation %s", name)
	return nil
}

// declaredPaths are the JSON member paths of fields ("data.files[].tool").
func declaredPaths(prefix string, fields []op.FieldInfo, out map[string]bool) {
	for _, f := range fields {
		p := prefix + f.Name
		out[p] = true
		switch f.Type {
		case op.TypeObject:
			declaredPaths(p+".", f.Fields, out)
		case op.TypeList:
			declaredPaths(p+"[].", f.Fields, out)
		case op.TypeStringMap:
			out[p+".*"] = true
		default:
		}
	}
}

// jsonPaths are the member paths of v; a string map's keys are "*".
func jsonPaths(prefix string, v json.RawMessage, declared map[string]bool, out map[string]bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(v, &obj) == nil {
		for k, sub := range obj {
			p := prefix + k
			if declared[strings.TrimSuffix(prefix, ".")+".*"] {
				p = prefix + "*"
			}
			out[p] = true
			jsonPaths(p+".", sub, declared, out)
		}
		return
	}
	var list []json.RawMessage
	if json.Unmarshal(v, &list) == nil {
		for _, item := range list {
			jsonPaths(strings.TrimSuffix(prefix, ".")+"[].", item, declared, out)
		}
	}
}

func checkDeclared(t *testing.T, what string, raw []byte, fields []op.FieldInfo, undeclared []string) {
	t.Helper()
	declared := map[string]bool{}
	declaredPaths("", fields, declared)
	got := map[string]bool{}
	jsonPaths("", raw, declared, got)
	var missing []string
	for p := range got {
		if !declared[p] && !slices.Contains(undeclared, p) {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%s: members the operation does not declare (so openapi.json misses them): %v", what, missing)
	}
}

// filled is a T with every field set (strings "x", numbers 1, bools true,
// times now, one item per list, one key per map), so encoding/json writes
// every member, omitempty or not.
func filled[T any](t *testing.T) T {
	t.Helper()
	var v T
	fill(reflect.ValueOf(&v).Elem())
	return v
}

var timeType = reflect.TypeFor[time.Time]()

func fill(v reflect.Value) {
	if v.Type() == timeType {
		v.Set(reflect.ValueOf(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)))
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(s.Index(0))
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fill(k)
		fill(e)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fill(v.Field(i))
			}
		}
	default:
	}
}
