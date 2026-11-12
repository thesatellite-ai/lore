package wire

// actor_once_test.go — later 153: cmd/lore writes the caller's actor row
// once per store it writes to (ensureActor: CreateWithID on the first
// write there, a Get afterwards, and nothing from a fresh App when the row
// is already in the store). Unlike the old binary, the repo store is not
// skipped: cmd/lore has no lore.db, so the repo's .lore/data is where its
// actor row lives (ensureActor's doc).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
)

// TestActorRowOncePerStore: two writes into each of two attached stores
// and the repo leave one actor row per store, byte-identical after the
// second write and after a fresh App's write; a store never written to
// gets none; the attached (non-client) stores keep the full row.
func TestActorRowOncePerStore(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	kb := folderStoreAt(t, "sto_0e45506e64357c94a87e70ef1c6b0001", "internal")
	kb2 := folderStoreAt(t, "sto_0e45506e64357c94a87e70ef1c6b0002", "internal")
	idle := folderStoreAt(t, "sto_0e45506e64357c94a87e70ef1c6b0003", "internal")
	writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+kb+"\n  - name: kb2\n    url: "+kb2+"\n  - name: idle\n    url: "+idle+"\n")
	internal := Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }}
	a := openWith(t, repo, internal)
	withProject(t, a)
	who, err := identity.ActorID(testEmail)
	if err != nil {
		t.Fatal(err)
	}
	dir := func(url string) string { return filepath.FromSlash(fileuri.LocalPath(url)) }
	files := map[string]string{
		"repo": filepath.Join(repo, ".lore", "data", schema.TableActors, who.String()+".json"),
		"kb":   filepath.Join(dir(kb), rowfs.StoreDataDir, schema.TableActors, who.String()+".json"),
		"kb2":  filepath.Join(dir(kb2), rowfs.StoreDataDir, schema.TableActors, who.String()+".json"),
	}
	write := func(app *App, to string) {
		t.Helper()
		in := `{"body":"note for ` + to + `"}`
		if to != "repo" {
			in = `{"body":"note for ` + to + `","to":"` + to + `"}`
		}
		if _, werr := run(t, app, opsmemory.OpAdd, in, true); werr != nil {
			t.Fatalf("write to %s: %v", to, werr)

		}
	}
	for _, to := range []string{"repo", "kb", "kb2"} {
		write(a, to)
	}
	first := map[string]string{}
	for name, p := range files {
		first[name] = string(mustRead(t, p))
		if name != "repo" && !strings.Contains(first[name], testEmail) {
			t.Fatalf("%s: a non-client store keeps the full row: %s", name, first[name])
		}
	}
	for _, to := range []string{"repo", "kb", "kb2"} {
		write(a, to)
	}
	b := openWith(t, repo, internal)
	for _, to := range []string{"kb", "kb2"} {
		write(b, to)
	}
	for name, p := range files {
		if got := string(mustRead(t, p)); got != first[name] {
			t.Fatalf("%s: the actor row was rewritten:\n%s\n---\n%s", name, first[name], got)
		}
		entries, rerr := os.ReadDir(filepath.Dir(p))
		if rerr != nil || len(entries) != 1 {
			t.Fatalf("%s: actor rows = %v, %v", name, entries, rerr)
		}
	}
	if _, serr := os.Stat(filepath.Join(dir(idle), schema.TableActors)); !os.IsNotExist(serr) {
		t.Fatalf("a store never written to got an actor row: %v", serr)
	}
}
