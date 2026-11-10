package stores

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestStoreDoctorChecksUsedStores (item 6): store doctor reports every
// store this repo and machine use, the personal store included, before
// the v0.1.17 org stores; a problem on any of them fails the run naming
// the store, a note never does; the old "direct pushes … confirmed on the
// next write" line is gone.
func TestStoreDoctorChecksUsedStores(t *testing.T) {
	t.Parallel()
	ok := StoreCheck{Store: "notes", Source: SourceStoresFile, Kind: StoreKindFolder, URL: "file:///placeholder/notes", Reachable: true}
	me := StoreCheck{Store: PersonalName, Source: SourcePersonal, Kind: StoreKindGit, URL: "https://example.test/acme/me.git", Reachable: true,
		Clone: "whole", Notes: []string{"ahead 0, behind 0 of origin/main as of the last fetch; last push 2026-01-02 03:04 UTC"}}
	f := &fakeEnv{checked: []StoreCheck{ok, me}}
	_, txt, err := invoke(t, f, OpDoctor, `{}`)
	want := "stores this repo and machine use:\n" +
		"✓ notes (stores.yaml, folder) file:///placeholder/notes\n" +
		"✓ me (personal, git) https://example.test/acme/me.git\n" +
		"    ahead 0, behind 0 of origin/main as of the last fetch; last push 2026-01-02 03:04 UTC\n" +
		"v0.1.17 org stores (orgs[].stores in ~/.lore/config.yaml):\n  none\n"
	if err != nil || txt != want {
		t.Fatalf("clean doctor:\n got %q %v\nwant %q", txt, err, want)
	}
	if strings.Contains(txt, "direct pushes") {
		t.Fatal("the confusing direct-pushes line is back")
	}

	me.Unpushed, me.Problems = 2, []string{"2 commit(s) not pushed, the oldest 3h old; the next lore command that reaches the store pushes them, or run `lore sync`"}
	f.checked = []StoreCheck{ok, me}
	if _, _, err = invoke(t, f, OpDoctor, `{}`); !errs.Has(err, errs.InvalidInput) ||
		!strings.Contains(err.Error(), "1 store(s) need attention: me: 2 commit(s) not pushed") {
		t.Fatalf("an unpushed personal store must fail the doctor: %v", err)
	}

	f.checked = nil
	if _, txt, err = invoke(t, f, OpDoctor, `{}`); err != nil || !strings.HasPrefix(txt, "stores this repo and machine use:\n  none") {
		t.Fatalf("no store: %q %v", txt, err)
	}
}
