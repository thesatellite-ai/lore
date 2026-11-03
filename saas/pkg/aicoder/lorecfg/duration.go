package lorecfg

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Day is the length of the `d` unit lore durations accept. Go's
// time.ParseDuration has no day unit, but the spec writes ages in days
// (`live.max_age: 7d`), so lore adds exactly this one unit: 24 hours, not a
// calendar day (no time zones, no DST — an age is a length, not a date).
const Day = 24 * time.Hour

// daySuffix is the one unit lore adds to Go's duration syntax.
const daySuffix = "d"

// Duration is a time.Duration that reads `7d`, `60s`, `1h30m` or a plain
// number of seconds from YAML, and writes the shortest of those forms.
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// ParseDuration parses Go duration syntax plus a whole-number `d` suffix
// (`7d`). A combined form such as `1d12h` is refused: one unit or Go's own
// syntax, so a reader never has to add two units in their head. Negative
// values are refused; every lore duration is a length.
func ParseDuration(s string) (Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
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

// String writes whole days as `Nd`, everything else in Go syntax.
func (d Duration) String() string {
	v := time.Duration(d)
	if v > 0 && v%Day == 0 {
		return strconv.FormatInt(int64(v/Day), 10) + daySuffix
	}
	return v.String()
}

// UnmarshalYAML implements yaml.Unmarshaler.
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

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

// UnmarshalText lets the TOML reader and flat sources read the same syntax.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = v
	return nil
}
