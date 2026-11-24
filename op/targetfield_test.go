package op_test

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

// storeTargetIn names its write target --store, its --to meaning someone
// (handoff add's shape, devco D-966).
type storeTargetIn struct {
	To    string `json:"to_actor,omitempty" flag:"to" desc:"who"`
	Store string `json:"store,omitempty" flag:"store" desc:"where"`
}

func (storeTargetIn) TargetField() string { return "store" }

// plainTargetIn is every other write: --to is the target.
type plainTargetIn struct {
	To string `json:"to,omitempty" flag:"to" desc:"where"`
}

// TestTargetFielderNamesTheTarget: an input declaring op.TargetFielder
// reports that field's name and its text as the named target (trimmed),
// never its --to; an input without one reports none (devco D-966).
func TestTargetFielderNamesTheTarget(t *testing.T) {
	t.Parallel()
	run := func(context.Context, storeTargetIn) (textOut, error) { return textOut{}, nil }
	e := op.Define(op.Spec{Name: "x.add", Summary: "x", Access: op.AccessWrite, Permission: op.PermWrite}, run)
	in, err := e.Decode(op.JSONDecoder{Body: []byte(`{"to_actor":"@bob","store":" acme/api "}`)})
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := in.TargetFieldName(); !ok || f != "store" {
		t.Errorf("TargetFieldName = %q, %v; want store, true", f, ok)
	}
	if n, ok := in.NamedTarget(); !ok || n != "acme/api" {
		t.Errorf("NamedTarget = %q, %v; want acme/api, true (the --store value, not --to's @bob)", n, ok)
	}
	plain := op.Define(op.Spec{Name: "y.add", Summary: "y", Access: op.AccessWrite, Permission: op.PermWrite},
		func(context.Context, plainTargetIn) (textOut, error) { return textOut{}, nil })
	pin, err := plain.Decode(op.JSONDecoder{Body: []byte(`{"to":"acme/api"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := pin.TargetFieldName(); ok {
		t.Errorf("an input without TargetFielder reports field %q", f)
	}
	if n, ok := pin.NamedTarget(); ok {
		t.Errorf("an input without TargetFielder/TargetNamer names target %q", n)
	}
}
