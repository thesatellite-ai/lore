package main

import (
	"os"
	"testing"

	"github.com/thesatellite-ai/lore/surface/cli"
)

// TestPaletteEnvMatrix: with both output streams piped (not a terminal),
// --color auto colours only when FORCE_COLOR is set and non-empty (any
// value, https://force-color.org) and NO_COLOR is unset or empty; NO_COLOR
// wins over FORCE_COLOR; always and never ignore both variables.
func TestPaletteEnvMatrix(t *testing.T) {
	pipe := func(t *testing.T) *os.File {
		t.Helper()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
		return w
	}
	unset := "<unset>"
	for _, c := range []struct {
		name, mode, force, noColor string
		want                       bool
	}{
		{"auto, pipe, nothing set", "", unset, unset, false},
		{"auto, pipe, FORCE_COLOR=1", "", "1", unset, true},
		{"auto, pipe, FORCE_COLOR=0 (any non-empty value)", "", "0", unset, true},
		{"auto, pipe, FORCE_COLOR empty", "", "", unset, false},
		{"auto, pipe, FORCE_COLOR=1 NO_COLOR=1", "", "1", "1", false},
		{"auto, pipe, FORCE_COLOR=1 NO_COLOR empty", "", "1", "", true},
		{"auto, pipe, NO_COLOR=1", "", unset, "1", false},
		{"--color auto given, FORCE_COLOR=1", "auto", "1", unset, true},
		{"always, pipe, NO_COLOR=1", "always", unset, "1", true},
		{"never, FORCE_COLOR=1", "never", "1", unset, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			for name, v := range map[string]string{envForceColor: c.force, envNoColor: c.noColor} {
				t.Setenv(name, "") // restored after the test
				if v == unset {
					if err := os.Unsetenv(name); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Setenv(name, v); err != nil {
					t.Fatal(err)
				}
			}
			oldOut, oldErr := os.Stdout, os.Stderr
			os.Stdout, os.Stderr = pipe(t), pipe(t)
			defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
			var f rootFlags
			if c.mode != "" {
				if err := f.color.Set(c.mode); err != nil {
					t.Fatal(err)
				}
			}
			for _, s := range []cli.Stream{cli.StreamOut, cli.StreamErr} {
				if got := f.palette(s).On; got != c.want {
					t.Errorf("stream %v: colour = %v, want %v", s, got, c.want)
				}
			}
		})
	}
}
