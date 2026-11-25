// cache.go — the `cache:` block of config.yaml (public issue #178): how long
// lore keeps a cache file nothing uses and how big the cache folder may
// grow before the least recently used files go. ops/cache applies both, in
// the once-a-day automatic pass and in `lore cache prune`.

package orgconf

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Defaults of the cache block.
const (
	// DefaultCacheMaxAge is how long a cache file may go unused before lore
	// removes it. Why 14 days: a repo a person opens every week or two keeps
	// its cache (a rebuild costs seconds per store), while the cache of a
	// throwaway clone, an agent's worktree or a store nobody reads any more
	// goes within two weeks.
	DefaultCacheMaxAge = 14 * Day
	// DefaultCacheMaxSize caps the whole cache folder. Why 2 GiB: a busy
	// machine's live set (a dozen repos, each with its repo cache and a
	// combined view of ~60 MB) stays well under it, while a machine that
	// opens hundreds of short-lived copies a week stops at a few percent of
	// a laptop's disk instead of growing without end.
	DefaultCacheMaxSize = 2 << 30
)

// Cache is the `cache:` block.
type Cache struct {
	// AutoPrune false turns the once-a-day automatic pass off (`lore cache
	// prune` still works by hand).
	AutoPrune *bool `yaml:"auto_prune,omitempty"`
	// MaxAge is cache.max_age: a cache file unused this long is removed.
	MaxAge *Duration `yaml:"max_age,omitempty"`
	// MaxSize is cache.max_size: above it the least recently used cache
	// files are removed.
	MaxSize *Size `yaml:"max_size,omitempty"`
}

// CacheAutoPrune is cache.auto_prune or its default (on).
func (c *Config) CacheAutoPrune() bool {
	return c == nil || c.Cache.AutoPrune == nil || *c.Cache.AutoPrune
}

// CacheMaxAge is cache.max_age or its default.
func (c *Config) CacheMaxAge() time.Duration {
	if c != nil && c.Cache.MaxAge != nil && c.Cache.MaxAge.D() > 0 {
		return c.Cache.MaxAge.D()
	}
	return DefaultCacheMaxAge
}

// CacheMaxSize is cache.max_size in bytes or its default.
func (c *Config) CacheMaxSize() int64 {
	if c != nil && c.Cache.MaxSize != nil && *c.Cache.MaxSize > 0 {
		return int64(*c.Cache.MaxSize)
	}
	return DefaultCacheMaxSize
}

// validate checks the cache block; messages are relative to `cache`.
func (k Cache) validate() []error {
	var all []error
	if k.MaxAge != nil && k.MaxAge.D() < time.Hour {
		all = append(all, errors.New("max_age: must be at least 1h (such as 14d)"))
	}
	if k.MaxSize != nil && *k.MaxSize < minCacheMaxSize {
		all = append(all, errors.New("max_size: must be at least 100MB (such as 2GB)"))
	}
	return all
}

// minCacheMaxSize keeps a typo such as `max_size: 2` (two bytes) from
// emptying the cache after every pass.
const minCacheMaxSize = 100 << 20

// Size is a config size in bytes, written as a whole number with an
// optional unit: 500MB, 2GB, 1.5GB, or bare bytes. KB, MB, GB and TB are
// powers of 1024 (the units `lore cache prune` prints).
type Size int64

// sizeUnits are the accepted suffixes, longest first so "GB" wins over "B".
var sizeUnits = []struct {
	suffix string
	mult   float64
}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}}

// ParseSize parses one config size.
func ParseSize(s string) (Size, error) {
	t := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	if t == "" {
		return 0, errors.New("empty size")
	}
	mult := 1.0
	for _, u := range sizeUnits {
		if strings.HasSuffix(t, u.suffix) {
			t, mult = strings.TrimSuffix(t, u.suffix), u.mult
			break
		}
	}
	n, err := strconv.ParseFloat(t, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q (use a number with a unit such as 500MB or 2GB)", s)
	}
	return Size(n * mult), nil
}

// UnmarshalYAML reads a scalar size.
func (z *Size) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: size must be a scalar such as 2GB", n.Line)
	}
	v, err := ParseSize(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*z = v
	return nil
}
