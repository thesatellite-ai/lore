package projresolve

import (
	"errors"
	"strings"
	"testing"

	"saas/pkg/aicoder/errcodes"
)

// TestExpandPath_TraversalIsBadPathWithHint pins the user-facing shape of the
// ".." refusal: code E_BAD_PATH (exit status 1, as docs/reference/errors.md
// lists), a hint that names the one-command opt-in, and the ErrBadPath
// sentinel still reachable through errors.Is for callers that match on it.
// Before this, the refusal was a bare sentinel that the CLI re-labelled
// E_INTERNAL, with no hint.
func TestExpandPath_TraversalIsBadPathWithHint(t *testing.T) {
	t.Setenv(EnvAllowDBPathOutside, "")
	cases := []string{
		"../client.db",
		"${PROJECT_ROOT}/../client.db",
		"./a/../b.db",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := ExpandPath(in, "/proj")
			var coded *errcodes.CLIError
			if !errors.As(err, &coded) {
				t.Fatalf("ExpandPath(%q) = %v; want an *errcodes.CLIError", in, err)
			}
			if coded.Code != errcodes.BadPath {
				t.Errorf("code = %s; want %s", coded.Code, errcodes.BadPath)
			}
			if !strings.Contains(coded.Hint, EnvAllowDBPathOutside+"=1") {
				t.Errorf("hint %q does not name %s=1", coded.Hint, EnvAllowDBPathOutside)
			}
			if !errors.Is(err, ErrBadPath) {
				t.Errorf("errors.Is(err, ErrBadPath) = false; the sentinel must stay reachable")
			}
		})
	}
}
