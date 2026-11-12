package cli

import (
	"context"
	"testing"
)

// TestNameActors: a "by act_…" in human output becomes the actor's name;
// an id the namer cannot name, or no namer, leaves the text as it is
// (bug hunt B3-24).
func TestNameActors(t *testing.T) {
	const nina = "act_0123456789abcdef0123456789abcdef"
	const other = "act_fedcba9876543210fedcba9876543210"
	text := "created 2026-11-02T10:00:05Z by " + nina + " · updated 2026-11-02T10:00:06Z by " + other + "\n"
	calls := 0
	c := config{actorNamer: func(context.Context) func(string) string {
		calls++
		return func(id string) string {
			if id == nina {
				return "Nina Newcomer"
			}
			return id
		}
	}}
	want := "created 2026-11-02T10:00:05Z by Nina Newcomer · updated 2026-11-02T10:00:06Z by " + other + "\n"
	if got := c.nameActors(context.Background(), text); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if c.nameActors(context.Background(), "no ids here\n"); calls != 1 {
		t.Errorf("the namer ran %d times; want once (never for text without an id)", calls)
	}
	if got := (config{}).nameActors(context.Background(), text); got != text {
		t.Errorf("without a namer: %q", got)
	}
}
