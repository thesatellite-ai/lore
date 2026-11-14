package wire

// leftgate_storepush_test.go — a push that opens a store clone by url
// (lore store push, and lore sync's push of org stores the repo does not
// attach) holds a removed person's later commits like an attached store's
// sync does (F-9, gate7a). orgsListingStore picks the orgs whose hold
// applies to a store: the org that lists it, never for another org's or
// the personal store. The end-to-end guard is TestJourneyStaffRemove.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestOrgsListingStoreNamesOnlyThatOrgsStores(t *testing.T) {
	home := t.TempDir()
	cfg := "orgs:\n  - name: northwind\n    stores:\n      - name: notes\n        url: https://example.com/northwind/notes\n" +
		"  - name: globex\n    stores:\n      - name: notes\n        url: https://example.com/globex/notes\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		org, url string
		want     bool
	}{
		{"northwind", "https://example.com/northwind/notes", true},
		{"northwind", "https://example.com/northwind/notes.git", true}, // compared normalised
		{"northwind", "https://example.com/globex/notes", false},       // another org's store
		{"globex", "https://example.com/globex/notes", true},
		{"northwind", "https://example.com/ana/personal", false}, // not an org store
		{"initech", "https://example.com/northwind/notes", false},
	} {
		if got := slices.Contains(orgsListingStore(home, c.url), c.org); got != c.want {
			t.Errorf("orgsListingStore(%s) has %s = %v, want %v", c.url, c.org, got, c.want)
		}
	}
	if got := orgsListingStore(t.TempDir(), "https://example.com/northwind/notes"); len(got) != 0 {
		t.Errorf("orgsListingStore with no config.yaml = %v, want none", got)
	}
}
