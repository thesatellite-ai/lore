package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigCheckUnknownKeyNamesLineAndKey: `lore config check` and `lore
// config set` warn about an unknown key with its line and its name only,
// at the top level and nested (live.foo keeps its own line), one warning
// per key; yaml.v3's "not found in type" and lore's Go type names never
// reach the user.
func TestConfigCheckUnknownKeyNamesLineAndKey(t *testing.T) {
	t.Parallel()
	const prefix = "unknown key, ignored: "
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"top level, first line", "org_foldr: x\n", []string{"line 1: org_foldr"}},
		{"top level, after a known block", "live:\n  autopush: true\nteam: x\n", []string{"line 3: team"}},
		{"nested under live", "live:\n  foo: 1\n", []string{"line 2: foo"}},
		{"nested, deeper line", "live:\n  autopush: true\n  defaults:\n    tasks: true\n  fetch_tll: 5m\n", []string{"line 5: fetch_tll"}},
		{"nested under store", "store:\n  lock_timeot: 30s\n", []string{"line 2: lock_timeot"}},
		{"two keys, two warnings", "nope: 1\nlive:\n  bar: 2\n", []string{"line 1: nope", "line 3: bar"}},
		{"key with a dash and digits", "x-key-2: 1\n", []string{"line 1: x-key-2"}},
		{"key with a space", "my key: 1\n", []string{"line 1: my key"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := standaloneConfig{home: home}.CheckConfig(context.Background())
			if err != nil {
				t.Fatalf("an unknown key is a warning, not an error: %v", err)
			}
			v, verr := standaloneConfig{}.ValidateConfig(context.Background(), []byte(tc.body))
			if verr != nil {
				t.Fatal(verr)
			}
			for surface, got := range map[string][]string{"check": out.Warnings, "set": v.Unknown} {
				var unknown []string
				for _, w := range got {
					if strings.HasPrefix(w, prefix) {
						unknown = append(unknown, strings.TrimPrefix(w, prefix))
					}
					if strings.Contains(w, unknownKeyNeedle) || strings.Contains(w, "configKeys") || strings.Contains(w, "orgconf.") {
						t.Errorf("%s: warning leaks yaml/Go wording: %q", surface, w)
					}
				}
				if strings.Join(unknown, "|") != strings.Join(tc.want, "|") {
					t.Errorf("%s: unknown-key warnings = %q, want %q", surface, unknown, tc.want)
				}
			}
		})
	}
}

// TestUnknownKeyText: yaml.v3's shape is taken apart; any other text is
// kept as it came (never emptied).
func TestUnknownKeyText(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"line 2: field team not found in type wire.configKeys":                                                    "line 2: team",
		"line 12: field foo not found in type orgconf.Live":                                                       "line 12: foo",
		"field team not found in type wire.configKeys":                                                            "field team not found in type wire.configKeys",
		"line 2: field lock_timeot not found in type struct { LockTimeout string \"yaml:\\\"lock_timeout\\\"\" }": "line 2: lock_timeot",
		"something else": "something else",
	} {
		if got := unknownKeyText(in); got != want {
			t.Errorf("unknownKeyText(%q) = %q, want %q", in, got, want)
		}
	}
}
