package storeaddr

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

const (
	idA  = "sto_01a1bf3249ed7319a7f26569ae4e79f6"
	idB  = "sto_01a1bf3249ed7319a7f26569ae4e7000"
	oldU = "git@git.example.test:acme/store.git"
	newU = "git@git.example.test:acme/store2.git"
)

func TestKeyAndSame(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"git@github.com:acme/notes.git", "ssh://git@github.com/acme/notes", true},
		{"https://github.com/acme/notes", "git@github.com:acme/notes.git", true},
		{"git@github.com:acme/notes", "git@github.com:acme/notes2", false},
		{"file:///tmp/me/", "file:///tmp/me", true},
		{"/tmp/me", "file:///tmp/me", true},
		{"file:///tmp/me", "file:///tmp/you", false},
	} {
		if got := Same(tc.a, tc.b); got != tc.same {
			t.Errorf("Same(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}
}

func TestSplits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		entries []Entry
		want    int
		primary string
	}{
		{"one address, written two ways", []Entry{
			{Place: PlaceStoresFile, Name: "acme", URL: oldU, StoreID: idA},
			{Place: PlaceOrg, Name: "acme", URL: "ssh://git@git.example.test/acme/store", StoreID: idA},
		}, 0, ""},
		{"two stores, two ids", []Entry{
			{Place: PlaceStoresFile, Name: "acme", URL: oldU, StoreID: idA},
			{Place: PlaceStoresFile, Name: "other", URL: newU, StoreID: idB},
		}, 0, ""},
		{"unknown id never splits", []Entry{
			{Place: PlaceStoresFile, Name: "acme", URL: newU},
			{Place: PlaceOrg, Name: "acme", URL: oldU, StoreID: idA},
		}, 0, ""},
		{"the machine config wins over stores.yaml", []Entry{
			{Place: PlaceStoresFile, Name: "acme", URL: newU, StoreID: idA},
			{Place: PlaceOrg, Name: "acme", URL: oldU, StoreID: idA},
		}, 1, oldU},
		{"the address record wins over the config", []Entry{
			{Place: PlaceStoresFile, Name: "acme", URL: oldU, StoreID: idA},
			{Place: PlaceOrg, Name: "acme", URL: oldU, StoreID: idA},
			{Place: PlaceRecord, URL: newU, StoreID: idA},
		}, 1, newU},
		{"stores.yaml only: the first entry", []Entry{
			{Place: PlaceStoresFile, Name: "a", URL: newU, StoreID: idA},
			{Place: PlaceStoresFile, Name: "b", URL: oldU, StoreID: idA},
		}, 1, newU},
	} {
		got := Splits(tc.entries)
		if len(got) != tc.want {
			t.Fatalf("%s: %d splits, want %d: %+v", tc.name, len(got), tc.want, got)
		}
		if tc.want == 1 && got[0].Primary != tc.primary {
			t.Fatalf("%s: primary %q, want %q", tc.name, got[0].Primary, tc.primary)
		}
	}
}

// TestRefusal: a write to the second address is E_REFUSED naming both
// addresses and the fix; the primary (in any spelling) writes.
func TestRefusal(t *testing.T) {
	t.Parallel()
	sp := Splits([]Entry{
		{Place: PlaceStoresFile, Name: "acme", URL: newU, StoreID: idA},
		{Place: PlaceOrg, Name: "acme", URL: oldU, StoreID: idA},
	})[0]
	if err := sp.Refusal("acme/org", "ssh://git@git.example.test/acme/store"); err != nil {
		t.Fatalf("the primary address was refused: %v", err)
	}
	if err := sp.Refusal("other", "git@git.example.test:acme/elsewhere.git"); err != nil {
		t.Fatalf("an address outside the split was refused: %v", err)
	}
	err := sp.Refusal("acme", newU)
	if !errs.Has(err, errs.Refused) {
		t.Fatalf("second address: %v, want E_REFUSED", err)
	}
	var le *errs.Error
	if !errors.As(err, &le) {
		t.Fatal("not a lore error")
	}
	for _, want := range []string{newU, oldU, idA} {
		if !strings.Contains(le.Message, want) {
			t.Errorf("message %q does not name %s", le.Message, want)
		}
	}
	if !strings.Contains(le.Hint, "lore store set-url "+idA+" "+oldU) {
		t.Errorf("hint %q does not give the set-url fix", le.Hint)
	}
	if got := sp.Line(); got != "store "+idA+" is reached at two addresses: "+oldU+", "+newU {
		t.Errorf("Line = %q", got)
	}
	if got := sp.Fix(); got != "lore store set-url "+idA+" "+oldU {
		t.Errorf("Fix = %q", got)
	}
}

// TestKeyDriveHost: file://C:/x (a drive written after file://) and
// file:///C:/x are one address; before, the first lost its drive.
func TestKeyDriveHost(t *testing.T) {
	t.Parallel()
	if a, b := Key("file://C:/Users/a/kb"), Key("file:///C:/Users/a/kb"); a != b || a != "file:///C:/Users/a/kb" {
		t.Fatalf("Key = %s and %s, want both file:///C:/Users/a/kb", a, b)
	}
}
