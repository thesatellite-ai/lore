package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestGoneRowPurgedOrDeleted (devco:D-590): a tombstone found by an id
// lookup answers E_PURGED when its folder's _purged.json lists the id (a
// purge is gone for good: never the trash list), and E_NOT_FOUND "deleted
// from <org>/<folder> on <date>" with the trash list command otherwise.
func TestGoneRowPurgedOrDeleted(t *testing.T) {
	t.Parallel()
	const id = "rul_0123456789abcdef0123456789abcdef"
	const place = "acme/sync"
	folder := filepath.Join(t.TempDir(), "sync")
	if err := os.MkdirAll(filepath.Join(folder, "rules"), 0o750); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(folder, "rules", id+".json")
	tomb := `{"_id":"` + id + `","_deleted":true,"_hlc":"2026-11-17T09:00:00Z/0/n1"}`
	if err := os.WriteFile(file, []byte(tomb), 0o600); err != nil {
		t.Fatal(err)
	}
	writeList := func(ids string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(folder, "_purged.json"), []byte(`{"_v":1,"ids":[`+ids+`]}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name, list string
		code       errs.Code
		say, never string
	}{
		{"no list", "", errs.NotFound, id + " was deleted from " + place + " on 2026-11-17", "E_PURGED"},
		{"listed elsewhere", `"rul_ffffffffffffffffffffffffffffffff"`, errs.NotFound, "lore trash list --store " + place, "purged"},
		{"purged", `"` + id + `"`, errs.Purged, id + " was purged from " + place + " on 2026-11-17", "trash list"},
	} {
		if tc.list != "" {
			writeList(tc.list)
		}
		live, gone := liveHit([]string{file}, id, place)
		if live || gone == nil {
			t.Fatalf("%s: liveHit = %v, %v; want a tombstone", tc.name, live, gone)
		}
		err := gone.err()
		env := errs.EnvelopeOf(err)
		text := env.Error.Message + " " + env.Error.Hint
		if !errs.Has(err, tc.code) || !strings.Contains(text, tc.say) || strings.Contains(text, tc.never) {
			t.Errorf("%s: %s %q, want %s saying %q, never %q", tc.name, env.Error.Code, text, tc.code, tc.say, tc.never)
		}
	}
}
