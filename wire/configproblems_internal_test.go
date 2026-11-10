package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Home names each config.yaml problem with its line and a fix (polish item
// 4, realmac #23): the identity key, a setups[] entry, a YAML syntax error,
// and nothing for a valid or absent file.
func TestConfigProblems(t *testing.T) {
	t.Parallel()
	const okSetup = "setups:\n  - name: personal\n    kind: solo\n    identity:\n      key: human:someone@example.test\n"
	for _, c := range []struct {
		name     string
		yaml     string // "" = no file
		wantN    int
		wantKey  string
		wantLine int
		fixHas   string
	}{
		{"absent", "", 0, "", 0, ""},
		{"valid", "identity:\n  key: human:someone@example.test\n" + okSetup, 0, "", 0, ""},
		{"bad identity key, a setup has a good one", "identity:\n  key: mira\n  name: A\n" + okSetup, 1, "identity.key", 2, "key: human:someone@example.test"},
		{"bad identity key, nothing to suggest", "identity:\n  name: A\n  key: mira\n", 1, "identity.key", 3, "human:<your email>"},
		{"bad setup folder", "setups:\n  - name: other\n    kind: solo\n  - name: personal\n    kind: solo\n    folders: [\"code/**\"]\n", 1, "setups[personal]", 4, "line 4"},
		{"yaml syntax error", "identity:\n\tkey: x\n", 1, "", 2, "line 2"},
		{"tab deeper in the file", "identity:\n    key: human:a@b.test\n\tname: x\n", 1, "", 3, "line 3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			if c.yaml != "" {
				if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(c.yaml), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			a := &App{opts: Options{Home: home}}
			got := a.ConfigProblems(context.Background())
			if len(got) != c.wantN {
				t.Fatalf("problems = %+v, want %d", got, c.wantN)
			}
			if c.wantN == 0 {
				return
			}
			p := got[0]
			if p.Key != c.wantKey || p.Line != c.wantLine {
				t.Fatalf("key/line = %q/%d, want %q/%d (%+v)", p.Key, p.Line, c.wantKey, c.wantLine, p)
			}
			if p.File == "" || p.Problem == "" || p.Fix == "" || !strings.Contains(p.Fix, c.fixHas) {
				t.Fatalf("problem %+v: want file, problem and a fix containing %q", p, c.fixHas)
			}
			if strings.HasPrefix(p.Problem, "orgconf:") || strings.HasPrefix(p.Problem, "wire:") {
				t.Fatalf("reader prefix left in %q", p.Problem)
			}
		})
	}
}
