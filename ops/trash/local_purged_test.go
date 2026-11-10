package trash_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/trash"
)

// purgeResultOf decodes a trash.purge result.
func purgeResultOf(t *testing.T, h *opstest.H, input string) trash.ResultOut {
	t.Helper()
	var out trash.ResultOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(trash.OpPurge, input))), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTrashPurgeLocalPurged pins `lore trash purge --local --purged`
// (DELETION 3.10, OV31, PASS3-A-4 on cmd/lore): it drops this machine's
// safety copies of rows the repo's _purged.json lists (a row purged on
// another machine) and only those; a copy of a row that is merely in the
// trash, or of an unknown id, stays. --dry-run and an unconfirmed run drop
// nothing (DQ2: only --confirm), a second run finds nothing, and the flags
// go together and take no other purge selector.
func TestTrashPurgeLocalPurged(t *testing.T) {
	h := opstest.New(t)
	// No lore.db at all: nothing to drop, no question asked.
	if out := purgeResultOf(t, h, `{"local":true,"purged":true}`); !out.Local || out.Count != 0 {
		t.Fatalf("no lore.db: %+v", out)
	}
	purged := trashed(t, h, "purged elsewhere", "")
	h.Must(trash.OpPurge, `{"target":"`+purged+`","confirm":true}`) // no lore.db yet: _purged.json lists it, no copy dropped
	deleted := trashed(t, h, "only in the trash", "")
	plantSyncTrash(t, h, [][3]string{
		{"memories", purged, "file deleted"}, {"memories", deleted, "file deleted"},
		{"memories", purged, "reverted"}, {"tasks", "tsk_unknown", "file deleted"},
	})

	for _, in := range []string{
		`{"local":true}`, `{"purged":true}`,
		`{"local":true,"purged":true,"target":"` + purged + `"}`,
		`{"local":true,"purged":true,"older_than":"1d"}`,
		`{"local":true,"purged":true,"expired":true}`,
		`{"local":true,"purged":true,"archived_before":"90d"}`,
	} {
		h.Fails(trash.OpPurge, in, errs.InvalidInput)
	}

	dry := purgeResultOf(t, h, `{"local":true,"purged":true,"dry_run":true}`)
	if !dry.Local || !dry.DryRun || dry.Count != 2 || dry.Action != "would drop" || len(dry.Rows) != 0 {
		t.Fatalf("dry run: %+v", dry)
	}
	for _, c := range dry.Copies {
		if c.RowID != purged {
			t.Fatalf("dry run lists a copy of a row that is not purged: %+v", c)
		}
	}
	if js := h.JSON(h.Must(trash.OpPurge, `{"local":true,"purged":true,"dry_run":true}`)); strings.Contains(js, "sk-secret-doc") {
		t.Fatalf("a copy's document leaked: %s", js)
	}
	h.Fails(trash.OpPurge, `{"local":true,"purged":true}`, errs.NeedsConfirm)
	if n := listOf(t, h, `{"local":true}`).Count; n != 4 {
		t.Fatalf("a dry run or an unconfirmed run dropped copies: %d left, want 4", n)
	}

	done := purgeResultOf(t, h, `{"local":true,"purged":true,"confirm":true}`)
	if done.Action != "dropped" || done.Count != 2 || done.DryRun {
		t.Fatalf("confirmed: %+v", done)
	}
	left := listOf(t, h, `{"local":true}`)
	if left.Count != 2 {
		t.Fatalf("left: %+v", left.Copies)
	}
	for _, c := range left.Copies {
		if c.RowID == purged {
			t.Fatalf("a copy of the purged row survived: %+v", c)
		}
	}
	again := opstest.Text(h.Must(trash.OpPurge, `{"local":true,"purged":true,"confirm":true}`))
	if again != "· no local copies of purged rows\n" {
		t.Fatalf("second run: %q", again)
	}
}

// TestTrashDoctorLocalPurged: the trash doctor warns with the count of
// this machine's copies of purged rows and names the drop command; no
// copies, no line; an unreadable _purged.json fails the check naming the
// file, never reads as "no purged rows" (FB-DOC-3).
func TestTrashDoctorLocalPurged(t *testing.T) {
	h := opstest.New(t)
	ctx := context.Background()
	const want = "trash: 2 local cop(ies) of purged rows on this machine; drop them with lore trash purge --local --purged --confirm"
	hasLine := func(sub string) bool {
		t.Helper()
		warnings, _, err := trash.Doctor(ctx, h.App)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range warnings {
			if strings.Contains(w, sub) {
				return true
			}
		}
		return false
	}
	if hasLine("purged rows") {
		t.Fatal("no lore.db: a purged-copies line")
	}
	purged := trashed(t, h, "purged elsewhere", "")
	h.Must(trash.OpPurge, `{"target":"`+purged+`","confirm":true}`)
	plantSyncTrash(t, h, [][3]string{{"memories", purged, "file deleted"}, {"memories", purged, "reverted"}, {"tasks", "tsk_other", "file deleted"}})
	if !hasLine(want) {
		t.Fatalf("missing doctor line %q", want)
	}
	if err := os.WriteFile(filepath.Join(h.Repo, ".lore", "data", "_purged.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The trash counts read the same list, so the whole check fails
	// naming the file (doctor prints the check's error), never a clean run.
	if _, _, err := trash.Doctor(ctx, h.App); err == nil || !strings.Contains(err.Error(), "_purged.json") {
		t.Fatalf("an unreadable _purged.json: err = %v, want one naming the file", err)
	}
	h.Fails(trash.OpPurge, `{"local":true,"purged":true,"confirm":true}`, errs.InvalidInput)
}
