package batch_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/batch"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestBatchUndoEditTwice: an edit batch's rows that already hold their
// values from before the batch are not a later edit. Undoing the batch a
// second time (or after each row was set back by hand) says "nothing to
// undo" and writes no new batch, like a delete batch; a row set back by
// hand beside an untouched one is skipped while the other is put back;
// a row edited to some other value still refuses with E_STALE.
func TestBatchUndoEditTwice(t *testing.T) {
	cases := []struct {
		name string
		// after runs between the bulk set and the undo under test; a and
		// b are the two rows the batch changed (owner ops → finance).
		after        func(h *opstest.H, bat, a, b string)
		wantText     []string
		wantSkipped  int
		wantStale    bool
		wantOwnerA   string
		wantOwnerB   string
		wantNewBatch bool
	}{
		{name: "undone already", after: func(h *opstest.H, bat, _, _ string) {
			h.MustYes(batch.OpUndo, `{"id":"`+bat+`"}`)
		}, wantText: []string{"nothing to undo in batch", "skipped 2", "already at its values before the batch"},
			wantSkipped: 2, wantOwnerA: "ops", wantOwnerB: "ops"},
		{name: "both set back by hand", after: func(h *opstest.H, _, a, b string) {
			for _, id := range []string{a, b} {
				h.MustYes(settings.OpMetaSet, `{"id":"`+id+`","pair":"owner=ops"}`)
			}
		}, wantText: []string{"nothing to undo in batch", "skipped 2"}, wantSkipped: 2, wantOwnerA: "ops", wantOwnerB: "ops"},
		{name: "one set back by hand, one untouched", after: func(h *opstest.H, _, a, _ string) {
			h.MustYes(settings.OpMetaSet, `{"id":"`+a+`","pair":"owner=ops"}`)
		}, wantText: []string{"restored 1 row to their values before", "skipped 1"}, wantSkipped: 1,
			wantOwnerA: "ops", wantOwnerB: "ops", wantNewBatch: true},
		{name: "edited to another value", after: func(h *opstest.H, _, a, _ string) {
			h.MustYes(settings.OpMetaSet, `{"id":"`+a+`","pair":"owner=later"}`)
		}, wantStale: true, wantOwnerA: "later", wantOwnerB: "finance"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := opstest.New(t)
			h.MustYes(settings.OpMetaAdd, `{"name":"pages","type":"list"}`)
			h.MustYes(settings.OpMetaAdd, `{"name":"owner","type":"text"}`)
			a := h.ID(h.MustYes(memAdd, `{"body":"a","meta":["pages=billing","owner=ops"]}`))
			b := h.ID(h.MustYes(memAdd, `{"body":"b","meta":["pages=billing","owner=ops"]}`))
			bat := batchOf(t, opstest.Text(h.MustYes(settings.OpMetaSet, `{"id":"owner=finance","where":["pages=billing"]}`)))
			tc.after(h, bat, a, b)
			if tc.wantStale {
				if msg := h.Fails(batch.OpUndo, `{"id":"`+bat+`"}`, errs.Stale); !strings.Contains(msg, a) || strings.Contains(msg, b) {
					t.Errorf("stale refusal names exactly the row edited since: %q", msg)
				}
			} else {
				res := h.MustYes(batch.OpUndo, `{"id":"`+bat+`"}`)
				txt := opstest.Text(res)
				for _, w := range tc.wantText {
					if !strings.Contains(txt, w) {
						t.Errorf("undo text %q lacks %q", txt, w)
					}
				}
				if got := undoHint.MatchString(txt); got != tc.wantNewBatch {
					t.Errorf("a new revert batch: got %v, want %v (%q)", got, tc.wantNewBatch, txt)
				}
				if n := strings.Count(txt, "already at its values before the batch"); n != tc.wantSkipped {
					t.Errorf("skipped rows: got %d, want %d (%q)", n, tc.wantSkipped, txt)
				}
			}
			for id, want := range map[string]string{a: tc.wantOwnerA, b: tc.wantOwnerB} {
				if got := metaOf(h, id); !strings.Contains(got, "owner\t"+want) {
					t.Errorf("%s: owner want %q, got %q", id, want, got)
				}
			}
		})
	}
}
