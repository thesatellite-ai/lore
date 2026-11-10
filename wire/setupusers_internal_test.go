package wire

import (
	"os"
	"path/filepath"
	"testing"
)

// setupUsers (polish item 3): a setup's GitHub account marks that login
// used; a solo setup without one (an import) marks its identity handle; a
// setup naming the account wins over another's handle.
func TestSetupUsers(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, yaml string
		want       map[string]string
	}{
		{"account", "setups:\n  - name: personal\n    kind: solo\n    github:\n      user: Octo\n", map[string]string{"octo": "personal"}},
		{"import: handle only", "setups:\n  - name: personal\n    kind: solo\n    identity:\n      handle: Octo\n", map[string]string{"octo": "personal"}},
		{"handle of a setup with an account is not used", "setups:\n  - name: personal\n    kind: solo\n    identity:\n      handle: octo\n    github:\n      user: other\n", map[string]string{"other": "personal"}},
		{"account wins over another's handle", "setups:\n  - name: imp\n    kind: solo\n    identity:\n      handle: octo\n  - name: real\n    kind: solo\n    github:\n      user: octo\n", map[string]string{"octo": "real"}},
		{"member handle ignored", "orgs:\n  - name: acme\nsetups:\n  - name: work\n    kind: member\n    org: acme\n", map[string]string{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(c.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			got := setupUsers(home)
			if len(got) != len(c.want) {
				t.Fatalf("setupUsers = %v, want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Fatalf("setupUsers = %v, want %v", got, c.want)
				}
			}
		})
	}
}
