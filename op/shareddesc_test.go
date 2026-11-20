package op

import (
	"strings"
	"testing"
)

// TestSharedDesc: devco:D-729. A "shared:<name>" desc tag is the one shared
// text (the --to help of every create says a linked repo writes its store
// folder), a plain tag is itself, an unknown name is refused.
func TestSharedDesc(t *testing.T) {
	got, ok := sharedDesc("shared:write-to")
	if !ok || got != DescWriteTo {
		t.Fatalf("shared:write-to = %q, %v; want DescWriteTo", got, ok)
	}
	if !strings.Contains(got, "linked to") || !strings.Contains(got, "repo") {
		t.Fatalf("the --to help does not name the linked default and repo: %q", got)
	}
	if got, ok := sharedDesc("@handle on the roster"); !ok || got != "@handle on the roster" {
		t.Fatalf("a plain tag changed: %q, %v", got, ok)
	}
	if _, ok := sharedDesc("shared:nope"); ok {
		t.Fatal("an unknown shared name is accepted")
	}
}
