package host

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/merge"
)

// TestMergeDriverReportsTrashOutcome (DELETION 3.7): when one side moved a
// row to the trash and the other side edited it, `lore merge-driver` keeps
// the row alive (delete_overruled) and says so on stderr, so the person
// merging hears that a delete lost; a plain field merge says nothing of
// the trash.
func TestMergeDriverReportsTrashOutcome(t *testing.T) {
	const rowPath = ".lore/data/memories/mem_01936f3300007d4e8f5a6b7c8d9e0f1a.json"
	ts := func(sec string) string { return "2026-11-05T10:00:0" + sec + ".000Z" }
	doc := func(sec, body, extra string) []byte {
		return []byte(`{"_v":2,"_hlc":"` + ts(sec) + `/0000/` + mdActorA.String() + `","_table":"memories","id":"` + mdRowID.String() +
			`","kind":"core","body":"` + body + `","updated_at":"` + ts(sec) + `","updated_by":"` + mdActorA.String() + `"` + extra + `}`)
	}
	trashed := `,"deleted_at":"` + ts("2") + `","deleted_by":"` + mdActorA.String() + `"`
	base := doc("1", "first", "")

	out, stderr, err := runDriver(t, rowPath, base, doc("2", "first", trashed), doc("3", "edited", ""))
	if err != nil {
		t.Fatalf("driver: %v (%s)", err, stderr)
	}
	if !strings.Contains(stderr, string(merge.TrashKeptEdit)) || !strings.Contains(stderr, rowPath) {
		t.Fatalf("a delete that lost to an edit must be reported on stderr: %q", stderr)
	}
	var top map[string]json.RawMessage
	if jerr := json.Unmarshal(out, &top); jerr != nil {
		t.Fatal(jerr)
	}
	if _, kept := top["delete_overruled"]; !kept || top["deleted_at"] != nil {
		t.Fatalf("the edited row must stay alive with delete_overruled:\n%s", out)
	}

	_, stderr, err = runDriver(t, rowPath, base, doc("2", "ours", ""), doc("3", "first", ""))
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	if strings.Contains(stderr, "kept alive") || strings.Contains(stderr, "deleted on") {
		t.Fatalf("a merge without a delete must not mention the trash: %q", stderr)
	}
}
