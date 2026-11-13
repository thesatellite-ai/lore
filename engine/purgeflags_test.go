package engine

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// TestPurgedText (ported from the old binary's / wire/host's
// TestPurgedLines): a purge rewrites the trash line and keeps the other
// lines; a plain delete given --confirm gets DQ8's note; a plain delete,
// or a confirm whose output has no trash line, is unchanged.
func TestPurgedText(t *testing.T) {
	t.Parallel()
	mid, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	id := mid.Any()
	line := "✓ mem_x moved to the trash (restore: lore trash restore --target mem_x)\n"
	purged := &PurgeRequest{Purged: []ids.AnyID{id}}
	cases := []struct {
		name, in string
		pr       *PurgeRequest
		confirm  bool
		want     string
	}{
		{"purge", line, purged, true, "✓ purged for good: " + id.String() + " (tombstones written; old content is still in git history)\n"},
		{"purge keeps other lines", line + "  scope: x\n", purged, true, "✓ purged for good: " + id.String() + " (tombstones written; old content is still in git history)\n  scope: x\n"},
		{"confirm note", line, &PurgeRequest{}, true, line + softDeleteNote},
		{"confirm without trash line", "✓ done\n", &PurgeRequest{}, true, "✓ done\n"},
		{"plain", line, &PurgeRequest{}, false, line},
	}
	for _, c := range cases {
		if got := purgedText(c.in, c.pr, c.confirm); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if got, want := purgeTarget(nil), "every matching row"; got != want {
		t.Errorf("purgeTarget(nil) = %q, want %q", got, want)
	}
	if got, want := purgeTarget([]string{"res_1"}), "res_1"; got != want {
		t.Errorf("purgeTarget = %q, want %q", got, want)
	}
}
