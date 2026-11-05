package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestCheckMachinePolicy pins P3-CFG-6 / FA-AGENT-6: an unknown policy key
// is a notice, a value its key's shape refuses (or a map) is
// E_CONFIG_INVALID naming policies.<key>; a valid map and no file pass.
func TestCheckMachinePolicy(t *testing.T) {
	cases := []struct {
		name, yaml string
		notice     string // a notice must contain this ("" = none)
		bad        string // the error must contain this ("" = no error)
	}{
		{name: "no file"},
		{name: "valid", yaml: "policies:\n  meta.max_bytes: 4096\n"},
		{name: "unknown key", yaml: "policies:\n  meta.max_bites: 4096\n", notice: "policies.meta.max_bites is not a policy key"},
		{name: "bad limit value", yaml: "policies:\n  meta.max_bytes: lots\n", bad: "policies.meta.max_bytes: "},
		{name: "bad rank value", yaml: "policies:\n  secrets.on_detect: shrug\n", bad: "policies.secrets.on_detect: "},
		{name: "map value", yaml: "policies:\n  meta.max_bytes: {a: 1}\n", bad: "policies.meta.max_bytes is not a plain value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.yaml != "" {
				if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(tc.yaml), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			notes, err := CheckMachinePolicy(home)
			switch {
			case tc.bad == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.bad != "" && (!errs.Has(err, errs.ConfigInvalid) || !strings.Contains(err.Error(), tc.bad)):
				t.Fatalf("error %v, want E_CONFIG_INVALID containing %q", err, tc.bad)
			}
			got := strings.Join(notes, "\n")
			if (tc.notice == "") != (got == "") || !strings.Contains(got, tc.notice) {
				t.Fatalf("notices %q, want %q", got, tc.notice)
			}
		})
	}
}
