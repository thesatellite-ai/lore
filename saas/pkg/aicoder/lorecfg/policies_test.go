package lorecfg

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestPoliciesAndStoreAreKnownKeys pins P3-CFG-6: policies: and
// store.lock_timeout decode with KnownFields (so `lore config check` does
// not call them unknown) and Validate checks their shape.
func TestPoliciesAndStoreAreKnownKeys(t *testing.T) {
	cases := []struct {
		name, yaml, bad string // bad "" = valid
	}{
		{name: "plain values", yaml: "policies:\n  meta.max_bytes: 4096\n  secrets.on_detect: refuse\n  resources.allowed_in: [store, repo]\nstore:\n  lock_timeout: 30s\n"},
		{name: "map value", yaml: "policies:\n  meta.max_bytes: {a: 1}\n", bad: "policies.meta.max_bytes: not a plain value"},
		{name: "list of maps", yaml: "policies:\n  resources.allowed_in: [{a: 1}]\n", bad: "policies.resources.allowed_in: a list may hold only plain values"},
		{name: "lock_timeout too long", yaml: "store:\n  lock_timeout: 1h\n", bad: `store: lock_timeout "1h"`},
		{name: "lock_timeout not a duration", yaml: "store:\n  lock_timeout: 7d\n", bad: `store: lock_timeout "7d"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec := yaml.NewDecoder(bytes.NewReader([]byte(tc.yaml)))
			dec.KnownFields(true)
			var c Config
			if err := dec.Decode(&c); err != nil {
				t.Fatalf("strict decode: %v", err)
			}
			err := c.Validate()
			switch {
			case tc.bad == "" && err != nil:
				t.Fatalf("Validate: %v", err)
			case tc.bad != "" && (err == nil || !strings.Contains(err.Error(), tc.bad)):
				t.Fatalf("Validate = %v, want %q", err, tc.bad)
			}
		})
	}
}
