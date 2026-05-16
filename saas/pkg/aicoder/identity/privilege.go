// Package identity contains the actor-resolution chain (8-step fallback) and
// privilege-related guards (refuse-root, sudo-PRESERVE_ENV detection).
//
// This file holds the privilege guards. The 8-step actor chain lives in
// resolve.go (added later when the actors ent schema is wired).
//
// Catches: R16 #14, R27 #7, R29 #36.
package identity

import (
	"errors"
	"fmt"
	"os"
)

// ErrRootRefused is returned when the binary is invoked with EUID 0 and the
// MINI_ALLOW_ROOT env-var override is NOT set.
var ErrRootRefused = errors.New("identity: refusing to run as root (set MINI_ALLOW_ROOT=1 to override)")

// ErrUIDMismatch is returned when EUID does not match the owner of $HOME,
// indicating sudo-with-PRESERVE_ENV — refusing to write to a HOME the binary
// can't trust to be its caller's.
var ErrUIDMismatch = errors.New("identity: EUID does not match HOME owner (sudo PRESERVE_ENV detected)")

// CheckPrivilege guards against running with elevated privileges that could
// damage the user's data:
//
//  1. Refuse if EUID == 0 (root) unless MINI_ALLOW_ROOT=1 is set.
//  2. Refuse if EUID != owner-of-$HOME (sudo with PRESERVE_ENV keeps user's
//     HOME but runs as root — writes would land in user's HOME but with
//     wrong ownership / wrong .ssh / etc.).
//
// Read-only commands are exempted (the caller decides).
//
// Returns nil if the privilege check passes, or one of the sentinel errors
// above. Caller wraps with errcodes.New(errcodes.RootRefused, ...) at the
// CLI layer.
func CheckPrivilege() error {
	euid := os.Geteuid()

	if euid == 0 {
		if os.Getenv("MINI_ALLOW_ROOT") != "1" {
			return ErrRootRefused
		}
		return nil // root explicitly allowed
	}

	// Non-root: confirm EUID matches HOME owner. If HOME is unset or stat
	// fails, skip the check (don't want to refuse on stripped CI envs).
	home, ok := os.LookupEnv("HOME")
	if !ok || home == "" {
		return nil
	}
	st, err := os.Stat(home)
	if err != nil {
		return nil // best-effort; HOME points nowhere accessible
	}
	homeUID, err := uidOf(st)
	if err != nil {
		return nil // platform doesn't expose ownership; skip
	}
	if homeUID != euid {
		return fmt.Errorf("%w: EUID=%d, HOME=%s owned by %d", ErrUIDMismatch, euid, home, homeUID)
	}
	return nil
}
