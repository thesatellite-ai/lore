package wire

// storepush_refused_ownname_test.go — one refusal record per store folder,
// whatever the place that synced called it (StorePushRefusal.Own): the
// folder "acme/web" is "acme" in a code repo and "acme/web" in the store
// checkout, and a conflict refused in one place and settled in the other
// left the first record standing, so `lore sync status` kept saying the
// push was refused after it landed.

import (
	"bytes"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

func TestStorePushRefusalOneRecordPerFolder(t *testing.T) {
	t.Parallel()
	m1, _ := refusedIDs(t)
	refused := store.SyncReport{Refused: []store.Refusal{{ID: m1, Code: errs.Stale, Reason: "both sides changed the row"}}}
	sync := func(res *OrgResolution, name store.Source, own string, rep store.SyncReport) {
		t.Helper()
		f := &fakePushBackend{caps: store.Capabilities{Remote: true}, rep: rep}
		b := &storePush{inner: f, name: name, own: own, res: res, notes: &bytes.Buffer{}, push: nowPush()}
		if _, err := b.Sync(background()); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("refused under one name, landed under another: cleared", func(t *testing.T) {
		t.Parallel()
		res := pushRes(t)
		sync(res, "acme/web", "acme/web", refused) // in the store checkout
		sync(res, "acme", "acme/web", store.SyncReport{Sent: 1})
		if got := refusalsAt(t, res.home); len(got) != 0 {
			t.Fatalf("the landed push left records: %+v", got)
		}
	})
	t.Run("refused under two names: one record", func(t *testing.T) {
		t.Parallel()
		res := pushRes(t)
		sync(res, "acme/web", "acme/web", refused)
		sync(res, "acme", "acme/web", refused)
		got := refusalsAt(t, res.home)
		if len(got) != 1 || got[0].Store != "acme" || got[0].Own != "acme/web" {
			t.Fatalf("records = %+v, want one, under the latest name", got)
		}
	})
	t.Run("stores of no org keep their own records", func(t *testing.T) {
		t.Parallel()
		res := pushRes(t)
		sync(res, "kb", "", refused)
		sync(res, "notes", "", store.SyncReport{Sent: 1})
		if got := refusalsAt(t, res.home); len(got) != 1 || got[0].Store != "kb" {
			t.Fatalf("records = %+v, want kb's alone", got)
		}
	})
}
