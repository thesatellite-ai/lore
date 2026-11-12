package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// v017Backend is a store opened with a v0.1.17 marker (no sto_ id): its
// Marker is what Open read; Dir is where `lore fix` later writes marker v2.
type v017Backend struct {
	store.Backend
	dir string
}

func (b v017Backend) Marker() rowfs.Marker { return rowfs.Marker{Version: 1} }
func (b v017Backend) Dir() string          { return b.dir }

// B4-17: a store that gets its sto_ id mid-run (lore fix) has one audit
// file under that id: what it wrote under name- before is folded in,
// oldest first, and the name- file is gone.
func TestStoreAuditAdoptsNameFile(t *testing.T) {
	t.Parallel()
	home, storeDir := t.TempDir(), t.TempDir()
	r := newRoutedAudit(&sinkLog{}, home, []attach.Opened{{Attached: engine.Attached{Name: "acme/org", Backend: v017Backend{dir: storeDir}}}})
	ctx := context.Background()
	rec := func(id string) {
		t.Helper()
		if err := r.Record(ctx, engine.Event{Action: engine.ActionCreate, Store: "acme/org", Table: row.Table("memories"), Op: id}); err != nil {
			t.Fatal(err)
		}
	}
	rec("before-fix") // no id yet: name-acme_org.jsonl
	auditDir := filepath.Join(home, filepath.FromSlash(StoreAuditDir))
	if _, err := os.Stat(filepath.Join(auditDir, "name-acme_org.jsonl")); err != nil {
		t.Fatalf("the id-less store did not write its name- file: %v", err)
	}
	m, err := rowfs.NewMarker(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := rowfs.WriteMarker(storeDir, m); err != nil { // what lore fix does
		t.Fatal(err)
	}
	rec("after-fix")
	entries, _ := os.ReadDir(auditDir)
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	want := m.ID.String() + storeAuditExt
	if len(names) != 1 || names[0] != want {
		t.Fatalf("audit files %v, want only %s", names, want)
	}
	b, err := os.ReadFile(filepath.Join(auditDir, want))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "before-fix") || !strings.Contains(lines[1], "after-fix") {
		t.Fatalf("merged audit file:\n%s", b)
	}
}

// A name- file an earlier run left beside a store that has its id now is
// folded in when the store opens (first run), before any write.
func TestStoreAuditAdoptsAtOpen(t *testing.T) {
	t.Parallel()
	home, storeDir := t.TempDir(), t.TempDir()
	m, err := rowfs.NewMarker(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := rowfs.WriteMarker(storeDir, m); err != nil {
		t.Fatal(err)
	}
	auditDir := filepath.Join(home, filepath.FromSlash(StoreAuditDir))
	if err := os.MkdirAll(auditDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old, cur := `{"action":"create","op":"old"}`+"\n", `{"action":"create","op":"new"}`+"\n"
	if err := os.WriteFile(filepath.Join(auditDir, "name-me.jsonl"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(auditDir, m.ID.String()+storeAuditExt), []byte(cur), 0o600); err != nil {
		t.Fatal(err)
	}
	newRoutedAudit(&sinkLog{}, home, []attach.Opened{{Attached: engine.Attached{Name: "me", Backend: v017Backend{dir: storeDir}}}})
	if _, err := os.Stat(filepath.Join(auditDir, "name-me.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("the name- file is still there: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(auditDir, m.ID.String()+storeAuditExt)); string(b) != old+cur {
		t.Fatalf("merged file %q, want %q", b, old+cur)
	}
}
