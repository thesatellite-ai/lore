package wire

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestCheckNewPlaceSpellings: `lore store init|add` refuse a place a
// stores.yaml entry already attaches, whatever the spelling of the url
// (scp, ssh://, https, .git, a trailing slash), the branch (--branch or
// ?branch=) or the folder (--folder or ?folder=); the refusal names the
// entry and both ways on. Another branch, folder or repo is a new place,
// and so is an explicit branch next to an entry that pins none (the
// default branch is the remote's to say).
func TestCheckNewPlaceSpellings(t *testing.T) {
	t.Parallel()
	const scp = "git@github.com:agency/notes"
	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores:\n"+
		"  - name: agency\n    url: "+scp+"\n"+
		"  - name: stag\n    url: https://github.com/agency/notes?branch=staging\n"+
		"  - name: tips\n    url: "+scp+"\n    folder: tips\n")
	a := openWith(t, repo, Options{})
	for _, c := range []struct {
		name  string
		place attach.Store
		dup   string // the entry it duplicates, "" for a new place
	}{
		{"same scp url", attach.Store{URL: scp}, "agency"},
		{"https spelling", attach.Store{URL: "https://github.com/agency/notes"}, "agency"},
		{".git suffix", attach.Store{URL: scp + ".git"}, "agency"},
		{"trailing slash", attach.Store{URL: "https://github.com/agency/notes/"}, "agency"},
		{"ssh:// spelling", attach.Store{URL: "ssh://git@github.com/agency/notes.git"}, "agency"},
		{"root folder spelled .", attach.Store{URL: scp, Folder: "."}, "agency"},
		{"--branch of a ?branch= entry", attach.Store{URL: scp, Branch: "staging"}, "stag"},
		{"?branch= of a ?branch= entry", attach.Store{URL: "https://github.com/agency/notes.git?branch=staging"}, "stag"},
		{"--folder", attach.Store{URL: "https://github.com/agency/notes", Folder: "tips"}, "tips"},
		{"?folder=", attach.Store{URL: "https://github.com/agency/notes?folder=tips"}, "tips"},
		{"folder with a trailing slash", attach.Store{URL: scp, Folder: "tips/"}, "tips"},
		{"another branch", attach.Store{URL: scp, Branch: "prod"}, ""},
		{"an explicit default branch name", attach.Store{URL: scp, Branch: "main"}, ""},
		{"another folder", attach.Store{URL: scp, Folder: "client"}, ""},
		{"a branch and a folder no entry has together", attach.Store{URL: scp, Folder: "tips", Branch: "staging"}, ""},
		{"another repo", attach.Store{URL: "git@github.com:agency/other"}, ""},
		{"another host", attach.Store{URL: "git@example.com:agency/notes"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := a.checkNewPlace(background(), c.place)
			if c.dup == "" {
				if err != nil {
					t.Fatalf("a new place was refused: %v", err)
				}
				return
			}
			var e *errs.Error
			if !errors.As(err, &e) || e.Code != errs.InvalidInput {
				t.Fatalf("want E_INVALID_INPUT, got %v", err)
			}
			if !strings.HasPrefix(e.Message, "store "+c.dup+" already attaches ") {
				t.Fatalf("the refusal must name entry %s: %q", c.dup, e.Message)
			}
			if e.Hint != "use --to "+c.dup+", or remove it first: lore store remove "+c.dup {
				t.Fatalf("hint %q", e.Hint)
			}
		})
	}
}

// TestStoreAddInitRefuseDuplicatePlace: through the operations, a second
// `lore store add` or `lore store init` of a store an entry already
// attaches is refused with stores.yaml unchanged (the message names the
// url and branch); a stores.yaml that already holds two entries for one
// place (written before this check) still loads and lists both.
func TestStoreAddInitRefuseDuplicatePlace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	u := fileURL(dir)
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	if _, err := run(t, a, opsstores.OpInit, `{"name":"kit","url":"`+u+`"}`, false); err != nil {
		t.Fatalf("store init kit: %v", err)
	}
	before, err := os.ReadFile(storesYAMLPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, op, body string }{
		{"add, same url", opsstores.OpAdd, `{"name":"kit2","url":"` + u + `"}`},
		{"add, trailing slash", opsstores.OpAdd, `{"name":"kit2","url":"` + u + `/"}`},
		{"init, same url", opsstores.OpInit, `{"name":"kit3","url":"` + u + `"}`},
	} {
		_, err := run(t, a, c.op, c.body, false)
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.InvalidInput || !strings.Contains(e.Message, "store kit already attaches "+u) ||
			!strings.Contains(e.Hint, "lore store remove kit") {
			t.Fatalf("%s: want the duplicate refusal, got %v", c.name, err)
		}
		after, rerr := os.ReadFile(storesYAMLPath(repo))
		if rerr != nil || string(after) != string(before) {
			t.Fatalf("%s: a refused command changed stores.yaml:\n%s", c.name, after)
		}
	}
	// an existing file with a duplicate still loads
	old := gitRepo(t)
	writeStoresYAML(t, old, "stores:\n  - name: kit\n    url: "+u+"\n  - name: kit2\n    url: "+u+"/\n")
	b := openWith(t, old, Options{})
	got, err := b.ListStores(background())
	if err != nil {
		t.Fatalf("a stores.yaml with a duplicate must still load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("list %+v, want both entries", got)
	}
	if _, err := b.Engine(background()); err != nil {
		t.Fatalf("the engine must open over a duplicate entry: %v", err)
	}
}
