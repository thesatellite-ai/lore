package wire

import (
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// TestSyncSetupHelpers: the store name without its folder, the one code
// of several failures, and a failed store's lines.
func TestSyncSetupHelpers(t *testing.T) {
	if got := storeNameOnly("acme/org", "org"); got != "acme" {
		t.Errorf("storeNameOnly = %q", got)
	}
	if got := storeNameOnly("notes-two", ""); got != "notes-two" {
		t.Errorf("storeNameOnly = %q", got)
	}
	if c := sameCode([]errs.Code{errs.StoreUnreachable, errs.StoreUnreachable}); c != errs.StoreUnreachable {
		t.Errorf("same: %s", c)
	}
	if c := sameCode([]errs.Code{errs.Stale, errs.StoreUnreachable}); c != errs.StoreUnreachable {
		t.Errorf("mixed with unreachable: %s", c)
	}
	if c := sameCode([]errs.Code{errs.Stale, errs.Internal}); c != errs.Stale {
		t.Errorf("mixed: %s", c)
	}
	lines := syncStoreLines(opssync.StoreReport{Store: "notes-two", Status: opssync.StoreStatusFailed, Error: "no such repo", Next: "check the url"})
	if len(lines) != 2 || lines[0] != "store notes-two: FAILED: no such repo" || lines[1] != "  next: check the url" {
		t.Errorf("failed lines: %q", lines)
	}
	lines = syncStoreLines(opssync.StoreReport{Store: "acme", Status: opssync.StoreStatusPulled, Received: 2})
	if len(lines) != 1 || lines[0] != "store acme: pulled (sent 0, received 2, merged 0)" {
		t.Errorf("pulled lines: %q", lines)
	}
}

// TestStoreIdentityFix: the fix names the clone and the identity, quoted
// for a shell.
func TestStoreIdentityFix(t *testing.T) {
	got := storeIdentityFix("/home/pat/.lore/stores/acme/notes", gitbackend.CloneIdentity{Signature: gitbackend.Signature{Name: "Pat Person", Email: "pat@work.example"}})
	want := "git -C /home/pat/.lore/stores/acme/notes config user.email pat@work.example && git -C /home/pat/.lore/stores/acme/notes config user.name 'Pat Person'"
	if got != want {
		t.Errorf("fix =\n%s\nwant\n%s", got, want)
	}
}
