package lorecfg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F19: a malformed legacy TOML is not migrated, stays in place, and both the
// Locate notice and the Load error name the line; a skip notice is explicit.
func TestMalformedTOMLNamesLineAndIsNotMigrated(t *testing.T) {
	cases := []struct {
		name string
		body string
		line int
	}{
		{"unterminated string on line 2", "display_name = \"Alice\"\nstable_key = \"unterminated\n", 2},
		{"bad key on line 1", "= \"x\"\n", 1},
		{"duplicate key on line 3 (no position from go-toml)", "a = 1\nb = 2\na = 3\n", 3},
		{"duplicate table after a multi-line array", "[t]\nxs = [\n  1,\n  2,\n]\n[t]\n", 6},
		{"duplicate key on line 12", strings.Repeat("# c\n", 10) + "k = 1\nk = 2\n", 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			base := "malformed-" + strings.ReplaceAll(tc.name, " ", "-")
			tomlPath := filepath.Join(dir, base+ExtTOML)
			if err := os.WriteFile(tomlPath, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			o := Options{Notices: &out}
			loc, err := Locate(context.Background(), dir, base, o)
			if err != nil {
				t.Fatalf("Locate: %v", err)
			}
			if loc.Path != tomlPath || loc.Format != FormatTOML {
				t.Fatalf("Locate = %+v, want the TOML", loc)
			}
			wantLine := fmt.Sprintf("line %d", tc.line)
			if !strings.Contains(out.String(), wantLine) || !strings.Contains(out.String(), "not migrated") {
				t.Fatalf("notice %q must name %q and say not migrated", out.String(), wantLine)
			}
			if strings.Contains(out.String(), "reading it as TOML") {
				t.Fatalf("notice %q promises a read that cannot succeed", out.String())
			}
			if _, err := os.Stat(filepath.Join(dir, base+ExtYAML)); !os.IsNotExist(err) {
				t.Fatal("a malformed TOML must not produce a YAML")
			}
			if _, err := os.Stat(tomlPath); err != nil {
				t.Fatal("a malformed TOML must be left in place")
			}
			_, err = Load[struct{}](context.Background(), loc, o)
			var pe *ParseError
			if !errors.As(err, &pe) || pe.Line != tc.line || pe.Path != tomlPath {
				t.Fatalf("Load err = %v, want *ParseError at line %d of %s", err, tc.line, tomlPath)
			}
			out.Reset()
			NoticeSkipped(o, tomlPath, err)
			if !strings.Contains(out.String(), "is skipped") || !strings.Contains(out.String(), fmt.Sprintf("line %d", tc.line)) {
				t.Fatalf("skip notice %q must say skipped and name the line", out.String())
			}
		})
	}
}
