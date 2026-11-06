package task

import (
	"regexp"
	"testing"
)

// TestRelationNames guards TASK 8.1's closed set: snake_case, unique.
func TestRelationNames(t *testing.T) {
	t.Parallel()
	snake := regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)
	seen := map[string]bool{}
	for _, r := range Relations() {
		if !snake.MatchString(r) || seen[r] {
			t.Fatalf("bad relation name %q", r)
		}
		seen[r] = true
	}
	if len(seen) != 3 {
		t.Fatalf("relations %v", Relations())
	}
}
