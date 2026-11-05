package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestRootRefusal (B11 port of saas/cmd/cli guard_cli_test.go
// TestRootRefusal): euid 0 is refused with E_ROOT_REFUSED (exit 23) naming
// the override; LORE_ALLOW_ROOT=1 allows it, any other value does not; a
// normal user is never refused.
//
// Not parallel: swaps the package-level geteuid and sets env vars.
func TestRootRefusal(t *testing.T) {
	orig := geteuid
	t.Cleanup(func() { geteuid = orig })
	geteuid = func() int { return 0 }
	for _, v := range []string{"", "0", "true", "yes"} {
		t.Setenv(envAllowRoot, v)
		err := refuseRoot()
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.RootRefused || errs.DefOf(e.Code).Exit != 23 || !strings.Contains(e.Hint, envAllowRoot+"=1") {
			t.Fatalf("%s=%q: root must be refused with E_ROOT_REFUSED naming the override: %v", envAllowRoot, v, err)
		}
	}
	t.Setenv(envAllowRoot, "1")
	if err := refuseRoot(); err != nil {
		t.Fatalf("%s=1 must allow: %v", envAllowRoot, err)
	}
	t.Setenv(envAllowRoot, "")
	for _, uid := range []int{501, 1000, -1} { // -1: os.Geteuid on Windows
		geteuid = func() int { return uid }
		if err := refuseRoot(); err != nil {
			t.Fatalf("uid %d refused: %v", uid, err)
		}
	}
}

// TestDiskFullHook: the hook every operation runs on failure turns
// SQLite's disk-full error into E_DISK_FULL and leaves others alone.
func TestDiskFullHook(t *testing.T) {
	t.Parallel()
	h := diskFullHook()
	full := errors.New("cache apply: database or disk is full (13)")
	if got := h.OnError(context.Background(), nil, full); !errs.Has(got, errs.DiskFull) {
		t.Fatalf("disk full: %v", got)
	}
	other := errs.New(errs.NotFound, "x")
	if got := h.OnError(context.Background(), nil, other); !errors.Is(got, other) || got.Error() != other.Error() {
		t.Fatalf("other error changed: %v", got)
	}
}
