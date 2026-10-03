package main

import (
	"errors"
	"strings"
	"testing"

	"saas/pkg/aicoder/errcodes"
)

// Not parallel: swaps the package-level geteuid and sets env vars.
func TestRootRefusal(t *testing.T) {
	orig := geteuid
	t.Cleanup(func() { geteuid = orig })
	geteuid = func() int { return 0 }
	t.Setenv(envAllowRoot, "")
	t.Setenv(envAllowRootLegacy, "")
	err := rootCmd.PersistentPreRunE(rootCmd, nil)
	if err == nil || !strings.Contains(err.Error(), string(errcodes.RootRefused)) {
		t.Fatalf("root must be refused: %v", err)
	}
	t.Setenv(envAllowRoot, envValueOn)
	if err := rootCmd.PersistentPreRunE(rootCmd, nil); err != nil {
		t.Fatalf("LORE_ALLOW_ROOT=1 must allow: %v", err)
	}
	t.Setenv(envAllowRoot, "")
	t.Setenv(envAllowRootLegacy, envValueOn)
	if err := rootCmd.PersistentPreRunE(rootCmd, nil); err != nil {
		t.Fatalf("legacy MINI_ALLOW_ROOT=1 must allow: %v", err)
	}
	geteuid = func() int { return 501 }
	t.Setenv(envAllowRootLegacy, "")
	if err := rootCmd.PersistentPreRunE(rootCmd, nil); err != nil {
		t.Fatalf("normal user refused: %v", err)
	}
}

func TestExplainStorageError(t *testing.T) {
	full := errors.New("create memory: database or disk is full (13)")
	if got := explainStorageError(full).Error(); !strings.Contains(got, string(errcodes.DiskFull)) {
		t.Fatalf("SQLITE_FULL must map to E_DISK_FULL: %s", got)
	}
	other := errors.New("some other failure")
	if explainStorageError(other) != other {
		t.Fatal("unrelated errors must pass through unchanged")
	}
	// "unable to open" only means disk-full when the disk really is full.
	cantOpen := errors.New("unable to open database file (14)")
	if explainStorageError(cantOpen) != cantOpen {
		t.Fatal("CANTOPEN with free space must pass through")
	}
}

func TestCheckNetworkFSOverride(t *testing.T) {
	cloud := t.TempDir() + "/Library/Mobile Documents/proj"
	t.Setenv(envAllowNetworkFS, "")
	if err := checkNetworkFS(cloud); err == nil || !strings.Contains(err.Error(), string(errcodes.NetworkFS)) {
		t.Fatalf("iCloud path must be refused: %v", err)
	}
	t.Setenv(envAllowNetworkFS, envValueOn)
	if err := checkNetworkFS(cloud); err != nil {
		t.Fatalf("override ignored: %v", err)
	}
}
