package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// TestForgetDropsTheStoresCopies: `lore store forget` removed the clone but
// left the store's row cache and its private renders, so a person who lost
// access (left the company, the contract ended) could still read its notes
// on the laptop. forgetCopies removes the cache folder, every private
// render of the store (both file name shapes) and its index.md line, and
// nothing of another store.
func TestForgetDropsTheStoresCopies(t *testing.T) {
	home := t.TempDir()
	gone, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	kept, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	id, other := gone.String(), kept.String()
	write := func(rel, body string) string {
		t.Helper()
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cache := write(cacheDir+"/"+cacheStoresDir+"/"+id+"/rows.db", "x")
	otherCache := write(cacheDir+"/"+cacheStoresDir+"/"+other+"/rows.db", "x")
	render := write(renderDirName+"/app-rep_1/acme-"+id+".md", "secret plan")
	oldRender := write(renderDirName+"/web-rep_2/"+id+".md", "secret plan")
	otherRender := write(renderDirName+"/app-rep_1/notes-"+other+".md", "keep")
	idx := write(renderDirName+"/app-rep_1/index.md", "repo app\n\n- acme-"+id+".md  store acme  git@x:a/b.git  internal\n- notes-"+other+".md  store notes  git@x:a/c.git  internal\n")
	if err := (&standaloneClones{home: home}).forgetCopies(id); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Dir(cache), render, oldRender} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived forget: %v", p, err)
		}
	}
	for _, p := range []string{otherCache, otherRender} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("another store's %s was removed: %v", p, err)
		}
	}
	b, err := os.ReadFile(idx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), id) || !strings.Contains(string(b), other) {
		t.Errorf("index.md after forget:\n%s", b)
	}
}
