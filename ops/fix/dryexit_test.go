package fix

import (
	"testing"

	"github.com/thesatellite-ai/lore/ops/doctor"
)

// TestDryOnlyWouldDo (B4-14): a dry run whose only findings are ones the
// real run's steps fix exits 0; any other warning, an error or a failed
// step keeps the doctor's exit.
func TestDryOnlyWouldDo(t *testing.T) {
	for _, c := range []struct {
		name string
		out  Out
		want bool
	}{
		{"stale renders only", Out{Status: doctor.StatusDegraded, Warnings: []string{"rendered_copy: .lore/LORE.md is stale"}}, true},
		{"another warning", Out{Status: doctor.StatusDegraded, Warnings: []string{"rendered_copy: x", "store_identity: y"}}, false},
		{"an error", Out{Status: doctor.StatusBroken, Errors: []string{"db: broken"}}, false},
		{"a failed step", Out{Status: doctor.StatusDegraded, Failed: []string{"migrate"}}, false},
	} {
		if got := dryOnlyWouldDo(c.out); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
