package main

import (
	"fmt"
	"testing"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/projresolve"
)

// TestMapResolveError_KeepsCodes pins that mapResolveError never re-labels an
// error that already has a code, and that the projresolve sentinels map to
// their codes even when wrapped. A ".." in db_path once surfaced as
// E_INTERNAL because the mapper compared with == and wrapped everything else.
func TestMapResolveError_KeepsCodes(t *testing.T) {
	coded := errcodes.New(errcodes.BadPath, "db_path has a `..` part").WithHint("h").WithCause(projresolve.ErrBadPath)
	cases := []struct {
		name string
		in   error
		want errcodes.Code
	}{
		{"coded bad path passes through", coded, errcodes.BadPath},
		{"wrapped coded error passes through", fmt.Errorf("load pointer: %w", coded), errcodes.BadPath},
		{"wrapped bad-path sentinel", fmt.Errorf("x: %w", projresolve.ErrBadPath), errcodes.BadPath},
		{"wrapped not-project-root sentinel", fmt.Errorf("x: %w", projresolve.ErrNotProjectRoot), errcodes.NotProjectRoot},
		{"wrapped ambiguous sentinel", fmt.Errorf("x: %w", projresolve.ErrAmbiguousProject), errcodes.AmbiguousProject},
		{"unknown error", fmt.Errorf("disk on fire"), errcodes.Internal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapResolveError(tc.in)
			if !errcodes.IsCode(got, tc.want) {
				t.Fatalf("mapResolveError(%v) = %v; want code %s", tc.in, got, tc.want)
			}
		})
	}
}
