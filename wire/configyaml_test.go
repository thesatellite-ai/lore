package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// configLeaks are texts a person must never see for their own YAML file:
// yaml's wording, its tags, Go type names, a quoted value.
var configLeaks = []string{"not found in type", "cannot unmarshal", "!!", "`", "wire.", "struct {", "identity."}

// TestConfigYAMLReadersNameLineAndKey: every reader of ~/.lore/config.yaml
// refuses a value of the wrong kind with E_CONFIG_INVALID naming the line
// and key path (top level and nested), a syntax error as "not valid YAML"
// with its line, and accepts unknown keys (also with a space: config check
// warns about those).
func TestConfigYAMLReadersNameLineAndKey(t *testing.T) {
	t.Parallel()
	readers := map[string]func(home string) error{
		"machine config": func(h string) error { _, err := readMachineConfig(h); return err },
		"lock timeout":   func(h string) error { _, err := readLockTimeout(h); return err },
		"machine policy": func(h string) error { _, _, err := machinePolicy(h); return err },
	}
	cases := []struct {
		name, body string
		want       map[string]string // reader -> wanted text; absent = no error
	}{
		{"store not a mapping (top level)", "store: fast\n", map[string]string{"lock timeout": "line 1: store: expected a mapping (key: value lines), got text"}},
		{"lock_timeout a list (nested)", "store:\n  lock_timeout: [30s]\n", map[string]string{"lock timeout": "line 2: store.lock_timeout: expected text, got a list"}},
		{"personal.store a mapping (nested)", "personal:\n  store:\n    url: x\n", map[string]string{"machine config": "line 3: personal.store: expected text, got a mapping"}},
		{"policies a list", "policies: [a]\n", map[string]string{"machine policy": "line 1: policies: expected a mapping (key: value lines), got a list"}},
		{"syntax error", "store: [\n", map[string]string{
			"machine config": "is not valid YAML: line 1", "lock timeout": "is not valid YAML: line 1", "machine policy": "is not valid YAML: line 1"}},
		{"unknown keys are accepted", "nope: 1\nmy key: 2\nstore:\n  lock timeout: 3\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			for name, read := range readers {
				err := read(home)
				want, bad := c.want[name]
				if !bad {
					if err != nil {
						t.Errorf("%s: %v, want no error", name, err)
					}
					continue
				}
				var e *errs.Error
				if !errors.As(err, &e) || e.Code != errs.ConfigInvalid || !strings.Contains(e.Message, want) {
					t.Errorf("%s: %v, want E_CONFIG_INVALID with %q", name, err, want)
					continue
				}
				for _, leak := range configLeaks {
					if strings.Contains(e.Message, leak) {
						t.Errorf("%s: message %q leaks %q", name, e.Message, leak)
					}
				}
			}
		})
	}
}

// TestValidateConfigDecodeNamesLineAndKey: `lore config set` refuses a
// candidate whose value has the wrong kind with its line and key path.
func TestValidateConfigDecodeNamesLineAndKey(t *testing.T) {
	t.Parallel()
	for body, want := range map[string]string{
		"live:\n  autopush: [1]\n":              "line 2: live.autopush: expected true or false, got a list",
		"orgs:\n  - name: a\n    live: maybe\n": "line 3: orgs[0].live: expected a mapping (key: value lines), got text",
	} {
		v, err := standaloneConfig{}.ValidateConfig(context.Background(), []byte(body))
		if err != nil || v.Decode == nil || !strings.Contains(v.Decode.Error(), want) {
			t.Errorf("%q: decode = %v (%v), want %q", body, v.Decode, err, want)
			continue
		}
		for _, leak := range configLeaks {
			if strings.Contains(v.Decode.Error(), leak) {
				t.Errorf("%q: %q leaks %q", body, v.Decode, leak)
			}
		}
	}
}

// TestModeBPointerNamesLineAndKey: a lore.yaml Mode B pointer with a value
// of the wrong kind names its line and key.
func TestModeBPointerNamesLineAndKey(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "lore.yaml")
	if err := os.WriteFile(path, []byte("db_path: /x/lore.db\nproject_id: [1]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readModeBPointerFile(path)
	var e *errs.Error
	if !errors.As(err, &e) || !strings.Contains(e.Message, "line 2: project_id: expected text, got a list") || strings.Contains(e.Message, "wire.") {
		t.Fatalf("err = %v", err)
	}
}
