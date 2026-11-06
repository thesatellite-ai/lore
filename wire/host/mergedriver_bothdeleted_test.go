package host

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/merge"
)

// TestMergeDriverBothDeletedOneStatement (TE-F-3): two deletes of one row
// meet in `lore merge-driver`; stderr says "deleted on both sides: the
// earlier delete kept" and never also "both sides changed deleted_…; kept
// the newer edit", which contradicted it. A clash on another field of the
// same row (kind) is still reported.
func TestMergeDriverBothDeletedOneStatement(t *testing.T) {
	const rowPath = ".lore/data/memories/mem_01936f3300007d4e8f5a6b7c8d9e0f1a.json"
	ts := func(sec string) string { return "2026-11-05T10:00:0" + sec + ".000Z" }
	doc := func(sec, kind, extra string) []byte {
		return []byte(`{"_v":2,"_hlc":"` + ts(sec) + `/0000/` + mdActorA.String() + `","_table":"memories","id":"` + mdRowID.String() +
			`","kind":"` + kind + `","body":"first","updated_at":"` + ts(sec) + `","updated_by":"` + mdActorA.String() + `"` + extra + `}`)
	}
	del := func(sec, reason string) string {
		return `,"deleted_at":"` + ts(sec) + `","deleted_by":"` + mdActorA.String() + `","deleted_reason":"` + reason + `"`
	}
	base := doc("1", "core", "")
	for _, c := range []struct {
		name          string
		ours, theirs  []byte
		wantNewerKind bool
	}{
		{"two deletes", doc("2", "core", del("2", "duplicate")), doc("3", "core", del("3", "stale")), false},
		{"two deletes, kind changed on both", doc("2", "episodic", del("2", "duplicate")), doc("3", "procedural", del("3", "stale")), true},
	} {
		_, stderr, err := runDriver(t, rowPath, base, c.ours, c.theirs)
		if err != nil {
			t.Fatalf("%s: driver: %v (%s)", c.name, err, stderr)
		}
		if !strings.Contains(stderr, string(merge.TrashDeletedBoth)) {
			t.Errorf("%s: two deletes must be reported: %q", c.name, stderr)
		}
		if strings.Contains(stderr, "deleted_") {
			t.Errorf("%s: the settled delete fields are not a newer-wins clash: %q", c.name, stderr)
		}
		if got := strings.Contains(stderr, "both sides changed kind; kept the newer edit"); got != c.wantNewerKind {
			t.Errorf("%s: kind clash reported = %v, want %v: %q", c.name, got, c.wantNewerKind, stderr)
		}
	}
}
