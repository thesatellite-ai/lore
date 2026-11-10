package op

import (
	"context"
	"slices"
	"testing"
)

// TestAliasTags: `alias:"store"` gives a flag another CLI spelling
// (FB-FLAG-STORE); an alias without a flag, one that repeats the flag, or
// one that collides with another field's flag is a tag mistake.
func TestAliasTags(t *testing.T) {
	t.Parallel()
	type ok struct {
		To string `json:"to,omitempty" flag:"to" alias:"store" desc:"the store"`
	}
	e := Define(Spec{Name: "memory.add", Summary: "s", Permission: PermWrite, Access: AccessWrite},
		func(context.Context, ok) (struct{}, error) { return struct{}{}, nil })
	f := e.InputFields()[0]
	if !slices.Equal(f.Spellings(), []string{"to", "store"}) {
		t.Fatalf("spellings %v", f.Spellings())
	}
	if (FieldInfo{}).Spellings() != nil {
		t.Fatal("a field with no flag has spellings")
	}
	run := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", name)
			}
		}()
		f()
	}
	type noFlag struct {
		A string `json:"a" alias:"b"`
	}
	type self struct {
		A string `json:"a" flag:"a" alias:"a"`
	}
	type clash struct {
		To    string `json:"to" flag:"to" alias:"store"`
		Store string `json:"store" flag:"store"`
	}
	type badName struct {
		A string `json:"a" flag:"a" alias:"B"`
	}
	run("no flag", func() {
		Define(Spec{Name: "x.a", Summary: "s", Permission: PermRead, Access: AccessRead}, func(context.Context, noFlag) (struct{}, error) { return struct{}{}, nil })
	})
	run("self", func() {
		Define(Spec{Name: "x.b", Summary: "s", Permission: PermRead, Access: AccessRead}, func(context.Context, self) (struct{}, error) { return struct{}{}, nil })
	})
	run("clash", func() {
		Define(Spec{Name: "x.c", Summary: "s", Permission: PermRead, Access: AccessRead}, func(context.Context, clash) (struct{}, error) { return struct{}{}, nil })
	})
	run("bad name", func() {
		Define(Spec{Name: "x.d", Summary: "s", Permission: PermRead, Access: AccessRead}, func(context.Context, badName) (struct{}, error) { return struct{}{}, nil })
	})
}
