package rowref

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestRelation pins the --relation rule: default, snake_case, reserved.
func TestRelation(t *testing.T) {
	reserved := map[string]string{"duplicates": "use task relate"}
	for _, c := range []struct {
		in, want string
		code     errs.Code
	}{
		{"", DefaultRelation, ""},
		{"affects", "affects", ""},
		{" about ", "about", ""},
		{"follow_up2", "follow_up2", ""},
		{"Affects", "", errs.InvalidIdentifier},
		{"a-b", "", errs.InvalidIdentifier},
		{"2x", "", errs.InvalidIdentifier},
		{"duplicates", "", errs.InvalidInput},
	} {
		got, err := Relation(c.in, reserved)
		if errs.CodeOf(err) != c.code || got != c.want {
			t.Errorf("Relation(%q) = %q, %v; want %q, %s", c.in, got, err, c.want, c.code)
		}
	}
}
