package wire

// auditchain_readonly_test.go — F-6 for the audit chain: on a lore home
// lore cannot write, reads of the chain (lore inbox's forced reveals,
// lore audit log) failed creating ~/.lore/state (inbox: E_INTERNAL). They
// now read the scratch copy; a record still goes to the real home and is
// refused naming it, so no audit event is kept in a temp folder and lost.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

func TestChainAuditReadOnlyHome(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through permissions")
	}
	t.Setenv("TMPDIR", t.TempDir())
	home := filepath.Join(t.TempDir(), ".lore")
	if err := os.Mkdir(home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	t.Cleanup(RemoveDryScratch)
	ctx := context.Background()

	sink, ok := NewChainAudit(home, t.TempDir(), time.Now).(*chainAudit)
	if !ok {
		t.Fatal("NewChainAudit is no *chainAudit")
	}
	if got, err := sink.AuditLog(ctx, 0); err != nil || len(got) != 0 {
		t.Fatalf("audit log on a read-only home = %v, %v; want an empty log", got, err)
	}
	if _, err := sink.AuditVerify(ctx); err != nil {
		t.Fatalf("audit verify on a read-only home: %v", err)
	}
	err := sink.Record(ctx, engine.Event{Action: engine.ActionSecretRuleChanged, Store: store.SourceRepo})
	if !errs.Has(err, errs.ReadOnlyFS) || !strings.Contains(err.Error(), home) {
		t.Fatalf("a record on a read-only home = %v; want E_READ_ONLY_FS naming %s", err, home)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("the read-only home was written: %d entries", len(entries))
	}

	a := &App{opts: Options{Home: home}}
	if err := a.HomeWritable(ctx); !errs.Has(err, errs.ReadOnlyFS) || !strings.Contains(errs.EnvelopeOf(err).Error.Message, home) {
		t.Fatalf("HomeWritable on a read-only home = %v; want E_READ_ONLY_FS naming it", err)
	}
	b := &App{opts: Options{Home: filepath.Join(t.TempDir(), ".lore")}}
	if err := b.HomeWritable(ctx); err != nil {
		t.Fatalf("HomeWritable on a writable home = %v", err)
	}
}
