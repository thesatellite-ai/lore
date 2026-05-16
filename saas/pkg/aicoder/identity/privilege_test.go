package identity

import (
	"errors"
	"os"
	"testing"
)

func TestCheckPrivilege_NormalUser(t *testing.T) {
	// We're not root in tests (CI runs as build user); HOME should be ours.
	if err := CheckPrivilege(); err != nil {
		t.Errorf("expected nil for normal user, got %v", err)
	}
}

func TestCheckPrivilege_RootRefused(t *testing.T) {
	// Skip if not root; we can't actually run as root in unit tests
	if os.Geteuid() != 0 {
		t.Skip("not root; can't test root-refusal path")
	}
	t.Setenv("MINI_ALLOW_ROOT", "")
	if err := CheckPrivilege(); !errors.Is(err, ErrRootRefused) {
		t.Errorf("expected ErrRootRefused, got %v", err)
	}
}

func TestCheckPrivilege_RootAllowedWithEnv(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("not root")
	}
	t.Setenv("MINI_ALLOW_ROOT", "1")
	if err := CheckPrivilege(); err != nil {
		t.Errorf("expected nil with MINI_ALLOW_ROOT=1, got %v", err)
	}
}

func TestCheckPrivilege_HomeUnset(t *testing.T) {
	t.Setenv("HOME", "")
	if err := CheckPrivilege(); err != nil {
		t.Errorf("expected nil when HOME unset, got %v", err)
	}
}

func TestCheckPrivilege_HomeNonExistent(t *testing.T) {
	t.Setenv("HOME", "/nonexistent/path/that/does/not/exist")
	if err := CheckPrivilege(); err != nil {
		t.Errorf("expected nil when HOME inaccessible, got %v", err)
	}
}

func TestCheckPrivilege_UIDMismatch(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; UIDMismatch test would fail differently")
	}
	// Fake HOME to /root which is owned by uid 0 — unless we ARE 0.
	// Skip on platforms where /root doesn't exist or is owned by current user.
	st, err := os.Stat("/root")
	if err != nil {
		t.Skip("cannot stat /root")
	}
	rootOwner, err := uidOf(st)
	if err != nil || rootOwner == os.Geteuid() {
		t.Skip("/root is not owned by a different uid")
	}

	t.Setenv("HOME", "/root")
	err = CheckPrivilege()
	if !errors.Is(err, ErrUIDMismatch) {
		t.Errorf("expected ErrUIDMismatch, got %v", err)
	}
}
