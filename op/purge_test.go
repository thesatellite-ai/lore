package op_test

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

type purgeIn struct {
	ID string `json:"id,omitempty" arg:"0" desc:"the row"`
	op.PurgeFlags
}

// otherConfirm has its own confirm field (trash purge's shape): it is not
// a delete's purge input.
type otherConfirm struct {
	Confirm bool `json:"confirm,omitempty" flag:"confirm" desc:"own confirm"`
}

type textOut struct {
	S string `json:"s" desc:"the line"`
}

func (o textOut) Text() string { return o.S }

// TestInputPurge: Purge reads the embedded PurgeFlags only (never an
// operation's own confirm field); Positionals names the row; a Result's
// text edits compose in order and leave the JSON alone.
func TestInputPurge(t *testing.T) {
	del := op.Define(op.Spec{Name: "thing.delete"}, func(_ context.Context, in purgeIn) (textOut, error) {
		return textOut{S: "line " + in.ID + "\n"}, nil
	})
	in, err := del.Decode(op.JSONDecoder{Body: []byte(`{"id":"x_1","purge":true,"confirm":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := in.Purge(); !ok || !f.Purge || !f.Confirm {
		t.Fatalf("Purge = %+v %v", f, ok)
	}
	if got := in.Positionals(); len(got) != 1 || got[0] != "x_1" {
		t.Fatalf("Positionals = %q", got)
	}
	bulk, err := del.Decode(op.JSONDecoder{Body: []byte(`{"purge":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if got := bulk.Positionals(); len(got) != 0 {
		t.Fatalf("empty id: Positionals = %q", got)
	}
	other := op.Define(op.Spec{Name: "thing.purge"}, func(context.Context, otherConfirm) (textOut, error) { return textOut{}, nil })
	oin, err := other.Decode(op.JSONDecoder{Body: []byte(`{"confirm":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := oin.Purge(); ok {
		t.Fatal("an own confirm field was read as PurgeFlags")
	}
	res, err := del.Invoke(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	res = res.WithTextEdit(func(s string) string { return s + "a\n" }).WithTextEdit(func(s string) string { return s + "b\n" })
	if got, ok := res.Text(); !ok || got != "line x_1\na\nb\n" {
		t.Fatalf("Text = %q %v", got, ok)
	}
	if b, _ := res.JSON(); string(b) != `{"s":"line x_1\n"}` {
		t.Fatalf("JSON edited: %s", b)
	}
}
