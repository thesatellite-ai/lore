package main

import (
	"errors"
	"fmt"
	"testing"

	"saas/pkg/aicoder/errcodes"
)

// TestErrorHint pins that the hint of a CLIError reaches the CLI however deep
// it is wrapped, and that errors without one print no hint line.
func TestErrorHint(t *testing.T) {
	withHint := errcodes.New(errcodes.OrgAmbiguous, "two entries match").WithHint("choose with lore org use <entry>")
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"direct", withHint, "choose with lore org use <entry>"},
		{"wrapped twice", fmt.Errorf("whoami: %w", fmt.Errorf("resolve: %w", withHint)), "choose with lore org use <entry>"},
		{"cli error without hint", errcodes.New(errcodes.OrgAmbiguous, "x"), ""},
		{"plain error", errors.New("boom"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := errorHint(c.err); got != c.want {
				t.Fatalf("errorHint = %q, want %q", got, c.want)
			}
		})
	}
}
