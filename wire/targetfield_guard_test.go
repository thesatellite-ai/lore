package wire

// targetfield_guard_test.go — devco D-966 (bug hunt H-225): `lore handoff
// add --store <store>/<folder>` in a repo linked to another folder refused
// "write the actor row to store <store>/<folder>: no store named … is
// attached". The handoff names its target --store (its --to is the old
// alias of --for), and the wire attached a `<store>/<folder>`, resolved
// team/company/folder names and routed outside a repo only for a field
// named `to`. Every write's target field is now one the wire reads: `to`,
// or the field the input names with op.TargetFielder.

import (
	"reflect"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/op"
	opshandoff "github.com/thesatellite-ai/lore/ops/handoff"
)

// fillDecoder decodes an input with every string field "x" (lists ["x"],
// numbers 1), so an input with required fields decodes without knowing
// them: the guard reads only which field is the target.
type fillDecoder struct{}

func (fillDecoder) Decode(dst reflect.Value, _ []op.FieldInfo) error {
	fill(dst)
	return nil
}

func fill(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			if f := v.Field(i); f.CanSet() || v.Type().Field(i).Anonymous {
				fill(f)
			}
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString("x")
		}
	case reflect.Slice:
		if v.CanSet() && v.Type().Elem().Kind() == reflect.String {
			v.Set(reflect.ValueOf([]string{"x"}).Convert(v.Type()))
		}
	case reflect.Int, reflect.Int64, reflect.Int32:
		if v.CanSet() {
			v.SetInt(1)
		}
	default:
	}
}

// TestEveryWriteTargetIsReadByTheWire: a write input field carrying the
// shared --to help (op.DescWriteTo: "where the row is written") is `to`,
// or the field its input names with op.TargetFielder, so prepareTarget,
// the aliases, the engine's call target and outside-repo routing all read
// it (D-966). A new write spelling its target another way fails here.
func TestEveryWriteTargetIsReadByTheWire(t *testing.T) {
	t.Parallel()
	a, err := New(Options{RepoDir: t.TempDir(), Home: t.TempDir(), FlagActor: testEmail, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	other := 0
	for _, e := range a.Registry().Entries() {
		s := e.Spec()
		if s.Access != op.AccessWrite {
			continue
		}
		for _, f := range e.InputFields() {
			if f.Desc != op.DescWriteTo || f.Name == engine.InputTarget {
				continue
			}
			other++
			in, derr := e.Decode(fillDecoder{})
			if derr != nil {
				t.Errorf("%s: decode a filled input: %v", s.Name, derr)
				continue
			}
			if got, ok := in.TargetFieldName(); !ok || got != f.Name {
				t.Errorf("%s: its write target is the field %q (--%s, the --to help) but the input does not name it with op.TargetFielder (got %q): the wire would not attach a <store>/<folder> given there, and its actor row would be refused (D-966)", s.Name, f.Name, f.Flag, got)
			}
		}
	}
	if other == 0 {
		t.Fatal("no write names its target outside `to` any more (handoff.add did): drop op.TargetFielder or this guard")
	}
}

// TestTypedTargetReadsHandoffStore: the wire reads handoff add's --store
// as its target and a rewrite of the target (team, a linked folder,
// `<store>/.`) reaches that field, never --to (the recipient).
func TestTypedTargetReadsHandoffStore(t *testing.T) {
	t.Parallel()
	a, err := New(Options{RepoDir: t.TempDir(), Home: t.TempDir(), FlagActor: testEmail, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	e, ok := a.Registry().Lookup(opshandoff.OpAdd)
	if !ok {
		t.Fatalf("no %s in the registry", opshandoff.OpAdd)
	}
	body := op.JSONDecoder{Body: []byte(`{"for":"@bob","to_actor":"team","body":"b","store":"team"}`)}
	in, err := e.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	if field, to := typedTarget(in); field != "store" || to != "team" {
		t.Errorf("typedTarget = %q, %q; want store, team", field, to)
	}
	var add opshandoff.AddIn
	if err := (aliasDecoder{inner: body, from: "team", to: "acme/web"}).Decode(reflect.ValueOf(&add).Elem(), e.InputFields()); err != nil {
		t.Fatal(err)
	}
	if add.Store != "acme/web" || add.To != "team" {
		t.Errorf("the alias rewrite gave store %q, to_actor %q; want store acme/web and the recipient field untouched", add.Store, add.To)
	}
}
