// duration.go — the durations org resolution reads from config.yaml
// (live.fetch_ttl, link_index.max_age), in v0.1.17's spelling: Go syntax
// (36h, 90s), a whole number of days (7d) or bare seconds (60).

package orgconf

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Day is 24 hours (the `d` suffix).
const Day = 24 * time.Hour

// daySuffix marks a duration written in days.
const daySuffix = "d"

// Defaults of the durations, as v0.1.17 (TEAM-025, F11).
const (
	// DefaultFetchTTL is how long a fetched store clone (and the repo's
	// live fetch) counts as fresh: a read within it makes no network call.
	// Owner item M-4: at v0.1.17's 60 s a person's reads minutes apart each
	// paid a fetch of every store over ssh (8 calls, ~25 s on a Mac).
	DefaultFetchTTL = 5 * time.Minute
	// DefaultLinkIndexMaxAge is how old a store copy may be and still
	// answer links while the store is unreachable.
	DefaultLinkIndexMaxAge = time.Hour
)

// Duration is a config duration.
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// ParseDuration parses one config duration.
func ParseDuration(s string) (Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty duration")
	}
	if strings.HasSuffix(s, daySuffix) {
		n, err := strconv.ParseInt(strings.TrimSuffix(s, daySuffix), 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q (use a whole number of days such as 7d, or Go syntax such as 36h)", s)
		}
		return Duration(time.Duration(n) * Day), nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("negative duration %q", s)
		}
		return Duration(time.Duration(n) * time.Second), nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if v < 0 {
		return 0, fmt.Errorf("negative duration %q", s)
	}
	return Duration(v), nil
}

// UnmarshalYAML reads a scalar duration.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: duration must be a scalar such as 7d or 60s", n.Line)
	}
	v, err := ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*d = v
	return nil
}

// Live is a `live:` block. Org resolution reads only FetchTTL; the other
// keys are declared so Validate checks them and a broken value fails every
// command closed (P3-CFG-5), as v0.1.17's lorecfg.Live did.
type Live struct {
	Defaults     map[string]bool `yaml:"defaults,omitempty"`
	MaxAge       *Duration       `yaml:"max_age,omitempty"`
	FetchTTL     *Duration       `yaml:"fetch_ttl,omitempty"`
	Autopush     *bool           `yaml:"autopush,omitempty"`
	CompactAfter *int            `yaml:"compact_after,omitempty"`
	Remote       string          `yaml:"remote,omitempty"`
}

// LinkIndex is the `link_index:` block (F11).
type LinkIndex struct {
	MaxAge *Duration `yaml:"max_age,omitempty"`
}

// FetchTTL is live.fetch_ttl (the entry's, else the top level's, else the
// default).
func (c *Config) FetchTTL(org *Org) time.Duration {
	if org != nil && org.Live != nil && org.Live.FetchTTL != nil {
		return org.Live.FetchTTL.D()
	}
	if c.Live.FetchTTL != nil {
		return c.Live.FetchTTL.D()
	}
	return DefaultFetchTTL
}

// LinkIndexMaxAge is link_index.max_age or its default.
func (c *Config) LinkIndexMaxAge() time.Duration {
	if c.LinkIndex.MaxAge != nil && c.LinkIndex.MaxAge.D() > 0 {
		return c.LinkIndex.MaxAge.D()
	}
	return DefaultLinkIndexMaxAge
}
