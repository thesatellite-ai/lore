package stores

import (
	"context"
	"strings"
	"testing"
)

// setupHintFake is a Manager whose setups bring org stores (SetupHinter).
type setupHintFake struct {
	fakeEnv
	setups []string
}

func (f *setupHintFake) OrgSetupNames(context.Context) []string { return f.setups }

// TestStoreListNamesSetups (devco D-554): store list in a folder no setup
// covers (only the personal store listed) names `store list --setup <s>`
// for every setup that brings org stores; a list that holds an org store
// gives no such hint.
func TestStoreListNamesSetups(t *testing.T) {
	f := &setupHintFake{fakeEnv: fakeEnv{stores: []Entry{{Name: "me", URL: "file:///s/me", Personal: true, Source: SourcePersonal}}}, setups: []string{"agency-admin", "agency-ops"}}
	raw, _, err := invoke(t, f, OpList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lore store list --setup agency-admin", "lore store list --setup agency-ops"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("store list outside every setup: want %q in %s", want, raw)
		}
	}
	f.stores = append(f.stores, Entry{Name: "agency", URL: "https://h/agency/notes.git", Source: SourceOrg})
	if raw, _, err = invoke(t, f, OpList, `{}`); err != nil || strings.Contains(raw, "--setup") {
		t.Fatalf("store list with an org store: %v, want no --setup hint in %s", err, raw)
	}
}
