package wire

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

// TestRemoveStoreFolderEntries (B2-sync, BUGFIX-01): a folder entry
// (stores.yaml "kb/acme", the name --to and store list use) is detached by
// that name; detaching the store kb detaches its folder entries too, and
// the question names them; a store whose name only shares the first
// letters (kb_other/acme) is left attached; an unknown folder entry is
// E_NOT_FOUND without a question.
func TestRemoveStoreFolderEntries(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	otherURL := folderStoreAt(t, otherStoreID, rowfs.VisibilityInternal)
	writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+url+"\n  - name: kb_other\n    url: "+otherURL+"\n")
	a := openWith(t, repo, Options{})
	for _, in := range []string{`{"store":"kb","folder":"acme"}`, `{"store":"kb","folder":"beta"}`, `{"store":"kb_other","folder":"acme"}`} {
		if _, err := run(t, a, opsstores.OpFolderAdd, in, true); err != nil {
			t.Fatal(err)
		}
	}
	writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+url+"\n"+
		"  - name: kb/acme\n    url: "+url+"\n    folder: acme\n  - name: kb/beta\n    url: "+url+"\n    folder: beta\n"+
		"  - name: kb_other\n    url: "+otherURL+"\n  - name: kb_other/acme\n    url: "+otherURL+"\n    folder: acme\n")
	names := func() []string {
		var out []string
		for _, s := range readStores(t, repo).Stores {
			out = append(out, string(s.Name))
		}
		return out
	}
	remove := func(name string, yes bool) (opsstores.RemoveOut, error) {
		var out opsstores.RemoveOut
		res, err := run(t, a, opsstores.OpRemove, `{"name":"`+name+`"}`, yes)
		if err != nil {
			return out, err
		}
		if jerr := json.Unmarshal([]byte(res), &out); jerr != nil {
			t.Fatal(jerr)
		}
		return out, nil
	}

	if _, err := remove("kb/nope", false); !errs.Has(err, errs.NotFound) {
		t.Fatalf("unknown folder entry: %v, want E_NOT_FOUND", err)
	}
	if out, err := remove("kb/beta", true); err != nil || out.Name != "kb/beta" || out.Folder != "beta" || len(out.Folders) != 0 {
		t.Fatalf("remove kb/beta: %+v %v", out, err)
	}
	if got, want := names(), []string{"kb", "kb/acme", "kb_other", "kb_other/acme"}; !slices.Equal(got, want) {
		t.Fatalf("after remove kb/beta: %v, want %v", got, want)
	}

	_, err := remove("kb", false)
	if !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(err.Error(), "kb/acme") {
		t.Fatalf("remove kb unconfirmed: %v, want E_NEEDS_CONFIRM naming kb/acme", err)
	}
	out, err := remove("kb", true)
	if err != nil || out.Name != "kb" || !slices.Equal(out.Folders, []string{"kb/acme"}) {
		t.Fatalf("remove kb: %+v %v", out, err)
	}
	if got, want := names(), []string{"kb_other", "kb_other/acme"}; !slices.Equal(got, want) {
		t.Fatalf("after remove kb: %v, want %v", got, want)
	}
	for _, s := range readStores(t, repo).Stores {
		if s.Name == store.Source("kb_other/acme") && s.URL != otherURL {
			t.Fatalf("kb_other/acme changed: %+v", s)
		}
	}
}
