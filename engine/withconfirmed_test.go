package engine

import (
	"context"
	"testing"
)

// TestWithConfirmed: inside a call, WithConfirmed marks it confirmed as
// --yes does; outside one it changes nothing.
func TestWithConfirmed(t *testing.T) {
	ctx := context.Background()
	if Confirmed(WithConfirmed(ctx)) {
		t.Fatal("outside a call WithConfirmed must change nothing")
	}
	in := context.WithValue(ctx, callKey{}, call{op: "x.y"})
	if Confirmed(in) || !Confirmed(WithConfirmed(in)) {
		t.Fatal("WithConfirmed must mark the running call confirmed")
	}
	if Confirmed(in) {
		t.Fatal("WithConfirmed changed the caller's ctx")
	}
}
