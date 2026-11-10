package trash_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/ops/trash"
)

// purgeIDs runs trash.purge with input and returns the sorted row ids it
// reports and whether it said dry_run.
func purgeIDs(t *testing.T, h *opstest.H, input string) ([]string, bool) {
	t.Helper()
	var out trash.ResultOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(trash.OpPurge, input))), &out); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(out.Rows))
	for _, r := range out.Rows {
		got = append(got, r.ID)
	}
	sort.Strings(got)
	if out.Count != len(got) {
		t.Errorf("%s: count %d, %d rows", input, out.Count, len(got))
	}
	return got, out.DryRun
}

// TestTrashPurgeArchivedBefore (DEL-ARCH, later 233 ops half): `lore trash
// purge --archived-before` through the operation: it is one of exactly one
// mode (with an id, --older-than or --expired: E_INVALID_INPUT); a bad
// date or age is E_INVALID_INPUT even with --confirm; without --confirm
// (also with --yes) it is E_NEEDS_CONFIRM naming --confirm and purges
// nothing; --dry-run lists exactly the rows archived before the cutoff
// (an alive one and one in the trash; not a row archived after it, not an
// unarchived row) and writes nothing; --confirm purges those (E_PURGED,
// gone from the trash), keeps the rest, and a second run purges nothing.
func TestTrashPurgeArchivedBefore(t *testing.T) {
	past := opstest.New(t, opstest.Fixed(time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)))
	old := past.ID(past.Must(memory.OpAdd, `{"body":"archived long ago"}`))
	past.Must(memory.OpArchive, `{"id":"`+old+`"}`)
	oldTrashed := past.ID(past.Must(memory.OpAdd, `{"body":"archived then deleted"}`))
	past.Must(memory.OpArchive, `{"id":"`+oldTrashed+`"}`)
	past.MustYes(memory.OpDelete, `{"id":"`+oldTrashed+`"}`)
	plain := past.ID(past.Must(memory.OpAdd, `{"body":"never archived"}`))

	h := past.Reopen() // the real clock: rows archived now
	recent := h.ID(h.Must(memory.OpAdd, `{"body":"archived today"}`))
	h.Must(memory.OpArchive, `{"id":"`+recent+`"}`)

	alive := func() {
		t.Helper()
		for _, id := range []string{old, plain, recent} {
			if err := liveGet(h, id); err != nil {
				t.Fatalf("%s: %v, want alive", id, err)
			}
		}
		if !inTrash(t, h, oldTrashed) {
			t.Fatal("the archived row in the trash left it")
		}
	}

	for _, in := range []string{
		`{"archived_before":"2026-01-01","target":"` + old + `","confirm":true}`,
		`{"archived_before":"2026-01-01","older_than":"1d","confirm":true}`,
		`{"archived_before":"2026-01-01","expired":true,"confirm":true}`,
		`{"archived_before":"soon","confirm":true}`,
		`{"archived_before":"-5d","confirm":true}`,
		`{"archived_before":"2026-13-01","confirm":true}`,
	} {
		h.Fails(trash.OpPurge, in, errs.InvalidInput)
	}
	for _, run := range []func(string, string) (op.Result, error){h.Run, h.RunYes} {
		_, err := run(trash.OpPurge, `{"archived_before":"2026-01-01"}`)
		if !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(strings.Join(errs.EnvelopeOf(err).Error.Next.Args, " "), "--archived-before 2026-01-01 --confirm") {
			t.Fatalf("without --confirm: %v", err)
		}
	}
	alive()

	want := []string{old, oldTrashed}
	sort.Strings(want)
	for _, cut := range []string{"2026-01-01", "30d"} {
		got, dry := purgeIDs(t, h, `{"archived_before":"`+cut+`","dry_run":true}`)
		if !dry || strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("dry run %s: %v dry=%v, want %v", cut, got, dry, want)
		}
	}
	if got, _ := purgeIDs(t, h, `{"archived_before":"2025-01-01","dry_run":true}`); len(got) != 0 {
		t.Fatalf("a cutoff before every archive lists %v", got)
	}
	alive()

	if got, dry := purgeIDs(t, h, `{"archived_before":"30d","confirm":true}`); dry || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("purge: %v dry=%v, want %v", got, dry, want)
	}
	for _, id := range want {
		if err := liveGet(h, id); !errs.Has(err, errs.Purged) && !errs.Has(err, errs.NotFound) {
			t.Errorf("%s after purge: %v, want E_PURGED", id, err)
		}
	}
	if inTrash(t, h, oldTrashed) {
		t.Error("a purged row is still in the trash")
	}
	for _, id := range []string{plain, recent} {
		if err := liveGet(h, id); err != nil {
			t.Errorf("%s: %v, want kept", id, err)
		}
	}
	if got, _ := purgeIDs(t, h, `{"archived_before":"30d","confirm":true}`); len(got) != 0 {
		t.Fatalf("a second run purged %v", got)
	}
}
