package stores

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestStoreDoctorTakesStore (devco D-633): `store doctor --store <org>`
// was "unknown flag" while every other store command names its store.
// It keeps the report to that store and its folders; a name nothing
// matches is refused with the stores doctor knows, never an empty report.
func TestStoreDoctorTakesStore(t *testing.T) {
	t.Parallel()
	org := StoreCheck{Store: "acme", Source: SourceOrg, Kind: StoreKindGit, URL: "https://example.test/acme/lore.git", Reachable: true, Clone: "whole"}
	sync := StoreCheck{Store: "acme/sync", Source: SourceOrg, Kind: StoreKindGit, URL: "https://example.test/acme/lore.git", Reachable: true, Clone: "whole"}
	me := StoreCheck{Store: PersonalName, Source: SourcePersonal, Kind: StoreKindGit, URL: "https://example.test/ada/me.git", Reachable: true, Clone: "whole",
		Problems: []string{"1 commit(s) not pushed"}}
	f := &fakeEnv{checked: []StoreCheck{org, sync, me}}
	_, txt, err := invoke(t, f, OpDoctor, `{"store":"acme"}`)
	if err != nil {
		t.Fatalf("store doctor --store acme: %v (the personal store's problem is not acme's)", err)
	}
	if !strings.Contains(txt, "acme (org, git)") || !strings.Contains(txt, "acme/sync (org, git)") || strings.Contains(txt, "me (personal") {
		t.Fatalf("store doctor --store acme kept the wrong stores:\n%s", txt)
	}
	if _, txt, err = invoke(t, f, OpDoctor, `{"store":"acme/sync"}`); err != nil || strings.Contains(txt, "✓ acme (org") {
		t.Fatalf("store doctor --store acme/sync: %v\n%s", err, txt)
	}
	if _, _, err = invoke(t, f, OpDoctor, `{"store":"globex"}`); !errs.Has(err, errs.NotFound) || !strings.Contains(errs.EnvelopeOf(err).Error.Hint, "acme, acme/sync, me") {
		t.Fatalf("an unknown store must be refused naming the known ones: %v", err)
	}
	if _, _, err = invoke(t, f, OpDoctor, `{}`); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("with no --store every store is checked (the personal store's problem fails it): %v", err)
	}
}
