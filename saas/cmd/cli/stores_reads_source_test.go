package main

import (
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestExternalSource: FLAG1 `_source` of a v0.1.17 cache row is the object
// with the store and folder names split out of the cache Source.
func TestExternalSource(t *testing.T) {
	cases := map[string]store.SourceRef{
		"store:northwind/acme": {Store: "northwind", Folder: "acme"},
		"store:northwind":      {Store: "northwind"},
		"personal:notes":       {Store: "me", Folder: "notes"},
		"repo":                 {Store: "repo"},
	}
	for in, want := range cases {
		if got := externalSource(in); got != want {
			t.Errorf("externalSource(%q) = %+v, want %+v", in, got, want)
		}
	}
}
