package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestStoreOpsFolderMatrix (CLI_REFERENCE store commands): on folder
// stores, init makes and attaches a store, add attaches an existing one
// (folder, read_only, a branch refused on a folder store), list shows
// them in order, remove detaches one, and every edit keeps the comments
// a person wrote in stores.yaml. Refusals write nothing: a reserved or
// bad name, a name used twice, an add of a folder that is not a store,
// remove of a name never attached.
func TestStoreOpsFolderMatrix(t *testing.T) {
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	const comment = "# kept: the team's stores\n"
	writeStoresYAML(t, repo, comment+"stores: []\n")
	yamlPath := filepath.Join(repo, filepath.FromSlash(attach.FileName))

	kbDir := t.TempDir()
	if _, err := run(t, a, opsstores.OpInit, `{"name":"kb","url":"file://`+filepath.ToSlash(kbDir)+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	other := folderStoreAt(t, shareStoreID, "shareable")
	if err := os.MkdirAll(filepath.Join(strings.TrimPrefix(other, "file://"), "acme", ".lore", "data"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, a, opsstores.OpAdd, `{"name":"shared","url":"`+other+`","folder":"acme","read_only":true}`, true); err != nil {
		t.Fatalf("add: %v", err)
	}
	listed, err := run(t, a, opsstores.OpList, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed, `"kb"`) || !strings.Contains(listed, `"shared"`) || strings.Index(listed, `"kb"`) > strings.Index(listed, `"shared"`) {
		t.Fatalf("list = %s, want kb then shared", listed)
	}
	body := string(mustRead(t, yamlPath))
	if !strings.HasPrefix(body, comment) || !strings.Contains(body, "read_only: true") || !strings.Contains(body, "folder: acme") {
		t.Fatalf("stores.yaml after init+add:\n%s", body)
	}

	refusals := []struct {
		name, op, in string
	}{
		{"reserved name", opsstores.OpAdd, `{"name":"team","url":"` + other + `"}`},
		{"me via add", opsstores.OpAdd, `{"name":"me","url":"` + other + `"}`},
		{"bad name", opsstores.OpAdd, `{"name":"Bad-Name","url":"` + other + `"}`},
		{"name used twice", opsstores.OpAdd, `{"name":"kb","url":"` + other + `"}`},
		{"not a store", opsstores.OpAdd, `{"name":"plain","url":"file://` + filepath.ToSlash(t.TempDir()) + `"}`},
		{"branch on a folder store", opsstores.OpAdd, `{"name":"br","url":"` + other + `","branch":"staging"}`},
		{"missing folder", opsstores.OpAdd, `{"name":"nofolder","url":"` + other + `","folder":"nope"}`},
		{"remove unknown", opsstores.OpRemove, `{"name":"never"}`},
	}
	for _, r := range refusals {
		t.Run(r.name, func(t *testing.T) {
			before := string(mustRead(t, yamlPath))
			_, rerr := run(t, a, r.op, r.in, true)
			if rerr == nil {
				t.Fatal("accepted, want a refusal")
			}
			if errs.Has(rerr, errs.Internal) {
				t.Fatalf("refused as E_INTERNAL (must be an input or state code): %v", rerr)
			}
			if after := string(mustRead(t, yamlPath)); after != before {
				t.Fatalf("a refusal changed stores.yaml:\n%s", after)
			}
		})
	}

	if _, err = run(t, a, opsstores.OpRemove, `{"name":"shared"}`, true); err != nil {
		t.Fatalf("remove: %v", err)
	}
	body = string(mustRead(t, yamlPath))
	if strings.Contains(body, "shared") || !strings.Contains(body, "name: kb") || !strings.HasPrefix(body, comment) {
		t.Fatalf("stores.yaml after remove:\n%s", body)
	}
	if _, err = os.Stat(filepath.Join(kbDir, "lore-store.yaml")); err != nil {
		t.Fatalf("init made no marker: %v", err)
	}
}
