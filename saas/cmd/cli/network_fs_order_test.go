package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"
)

// TestMaybeMaterialize_NetworkFSRefusesBeforeWriting pins the order of a
// fresh clone inside a cloud-synced folder: E_NETWORK_FS comes first and
// nothing is written into .lore/ (no lore.db, no state dir, no backup). A
// clone in a Dropbox folder once got .lore/lore.db and a backup built there
// and only then the refusal. The opt-in (LORE_ALLOW_NETWORK_FS=1) still
// builds the cache, so the check is a refusal, not a lost feature.
//
// Not parallel: it changes the working directory and environment.
func TestMaybeMaterialize_NetworkFSRefusesBeforeWriting(t *testing.T) {
	t.Setenv(envAllowNetworkFS, "")
	t.Setenv(envSync, "")
	t.Setenv(envSyncQuiet, envValueOn)
	t.Setenv(envDBPath, "")

	root := filepath.Join(t.TempDir(), "Dropbox", "site")
	data := filepath.Join(root, projresolve.MarkerDir, lsync.DataDirName)
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, lsync.MetaFileName), []byte(`{"_v":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	err := maybeMaterialize(context.Background(), &commonFlags{})
	if !errcodes.IsCode(err, errcodes.NetworkFS) {
		t.Fatalf("maybeMaterialize = %v; want E_NETWORK_FS", err)
	}
	if got := materializeError(err); !errcodes.IsCode(got, errcodes.NetworkFS) {
		t.Fatalf("materializeError relabelled the refusal: %v", got)
	}
	entries, rerr := os.ReadDir(filepath.Join(root, projresolve.MarkerDir))
	if rerr != nil {
		t.Fatal(rerr)
	}
	for _, e := range entries {
		if e.Name() != lsync.DataDirName {
			t.Errorf(".lore/%s was written before the refusal; nothing but data/ may exist", e.Name())
		}
	}

	t.Setenv(envAllowNetworkFS, envValueOn)
	if err := maybeMaterialize(context.Background(), &commonFlags{}); err != nil {
		t.Fatalf("with %s=1: %v", envAllowNetworkFS, err)
	}
	if _, err := os.Stat(filepath.Join(root, projresolve.MarkerDir, projresolve.ModeAFile)); err != nil {
		t.Fatalf("with %s=1 lore.db must be built: %v", envAllowNetworkFS, err)
	}
}
