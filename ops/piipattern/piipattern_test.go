package piipattern

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// fakeEnv returns rows for Table from the machine store and records the read.
type fakeEnv struct {
	rows   []row.Row
	source store.Source
	table  row.Table
}

func (f *fakeEnv) List(_ context.Context, source store.Source, table row.Table) ([]row.Row, error) {
	f.source, f.table = source, table
	return f.rows, nil
}

// invoke runs the operation named name with a JSON input.
func invoke(t *testing.T, env Env, name, input string) (op.Result, error) {
	t.Helper()
	for _, e := range Ops(env) {
		if e.Spec().Name != name {
			continue
		}
		in, err := e.Decode(op.JSONDecoder{Body: []byte(input)})
		if err != nil {
			t.Fatal(err)
		}
		return e.Invoke(context.Background(), in)
	}
	t.Fatalf("no operation %s", name)
	return op.Result{}, nil
}

// text is value.TextOf for a known-good literal.
func text(t *testing.T, s string) value.Text {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// pip builds a pii_patterns row.
func pip(t *testing.T, id, name string, enabled bool, at time.Time) row.Row {
	t.Helper()
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	f := map[string]value.Value{fieldName: text(t, name), fieldRegex: text(t, "x[0-9]+"), fieldSourceKind: text(t, "user"), fieldEnabled: value.BoolOf(enabled)}
	return row.Row{Table: Table, ID: aid, Fields: f, CreatedAt: at, UpdatedAt: at}
}

// TestListReadsMachineStoreByName (P3-PII-1): list reads Table from the
// machine store, orders by name (id on ties), keeps the v0.1.17 JSON
// envelope and the on/off text; no rows is an empty list, never null.
func TestListReadsMachineStoreByName(t *testing.T) {
	at := time.Date(2025, 5, 1, 10, 0, 0, 0, time.UTC)
	f := &fakeEnv{rows: []row.Row{
		pip(t, "pip_0190e6a0c7d27b4c8a1e2f3a4b5c6d72", "beta", false, at),
		pip(t, "pip_0190e6a0c7d27b4c8a1e2f3a4b5c6d71", "alpha", true, at),
		pip(t, "pip_0190e6a0c7d27b4c8a1e2f3a4b5c6d70", "beta", true, at),
	}}
	res, err := invoke(t, f, OpList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if f.source != store.SourceMachine || f.table != Table {
		t.Errorf("read %s/%s, want the machine store's %s", f.source, f.table, Table)
	}
	b, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var out ListOut
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, p := range out.Data {
		got = append(got, p.Name+"/"+p.ID[len(p.ID)-2:])
	}
	if want := "[alpha/71 beta/70 beta/72]"; out.Count != 3 || fmt.Sprint(got) != want {
		t.Errorf("order = %v (count %d), want %s", got, out.Count, want)
	}
	if out.Data[0].CreatedAt != "2025-05-01T10:00:00Z" || !out.Data[0].Enabled || out.Data[0].Regex != "x[0-9]+" {
		t.Errorf("first = %+v", out.Data[0])
	}
	if want := "[on] alpha  (user)\n[on] beta  (user)\n[off] beta  (user)\n"; out.Text() != want {
		t.Errorf("text = %q, want %q", out.Text(), want)
	}

	res, err = invoke(t, &fakeEnv{}, OpList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if b, err = res.JSON(); err != nil || !strings.Contains(string(b), `"data":[]`) {
		t.Errorf("empty list JSON = %s, %v", b, err)
	}
}

// TestMovedVerbsRefuse: add, enable and disable refuse with E_INVALID_INPUT
// naming the secrets rule command, before reading anything.
func TestMovedVerbsRefuse(t *testing.T) {
	for name, repl := range map[string]string{OpAdd: "lore secrets rule add", OpEnable: "lore secrets rule on", OpDisable: "lore secrets rule off"} {
		f := &fakeEnv{}
		input := `{"id":"pip_x"}`
		if name == OpAdd {
			input = `{"name":"n","regex":"r"}`
		}
		_, err := invoke(t, f, name, input)
		if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), repl) {
			t.Errorf("%s: err = %v, want E_INVALID_INPUT naming %s", name, err, repl)
		}
		if f.table != "" {
			t.Errorf("%s read %s", name, f.table)
		}
	}
}
