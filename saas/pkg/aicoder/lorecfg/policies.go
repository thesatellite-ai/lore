package lorecfg

// policies.go — the config.yaml keys the 0.1.18 core reads that v0.1.17
// did not know: `policies:` (the machine level of policy, FA-AGENT-6) and
// `store.lock_timeout` (FA-AGENT-8). Declaring them keeps `lore config
// check` from calling them unknown (P3-CFG-6); Validate checks their
// shape here, and the core checks a policy value against its key
// (wire.CheckMachinePolicy, which `lore config check` also runs).

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// MaxLockTimeout caps store.lock_timeout (the core's wire maxLockTimeout):
// a typo like 10h must not hang every command behind a stuck lock.
const MaxLockTimeout = 10 * time.Minute

// StoreSettings is the `store:` block of config.yaml.
type StoreSettings struct {
	// LockTimeout is a Go duration in (0, MaxLockTimeout]; "" = the
	// backends' default.
	LockTimeout string `yaml:"lock_timeout,omitempty"`
}

// validate checks store.lock_timeout as the core reads it.
func (s StoreSettings) validate() []error {
	if s.LockTimeout == "" {
		return nil
	}
	d, err := time.ParseDuration(s.LockTimeout)
	if err != nil || d <= 0 || d > MaxLockTimeout {
		return []error{fmt.Errorf("lock_timeout %q: not a duration between 0 and 10m (e.g. 30s)", s.LockTimeout)}
	}
	return nil
}

// validatePolicies checks that every policies value is plain: a scalar or
// a list of scalars (the core's wire yamlValue accepts nothing else).
// Whether the key is known and the value fits it is the core's check.
func (c *Config) validatePolicies() []error {
	keys := make([]string, 0, len(c.Policies))
	for k := range c.Policies {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []error
	for _, k := range keys {
		if err := plainPolicyValue(c.Policies[k]); err != nil {
			out = append(out, fmt.Errorf("policies.%s: %w", k, err))
		}
	}
	return out
}

// plainPolicyValue refuses a map, or a list holding anything but scalars.
func plainPolicyValue(n yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		return nil
	case yaml.SequenceNode:
		for _, item := range n.Content {
			if item.Kind != yaml.ScalarNode {
				return errors.New("a list may hold only plain values")
			}
		}
		return nil
	default:
		return errors.New("not a plain value (a bool, a number, a string or a list of strings)")
	}
}
