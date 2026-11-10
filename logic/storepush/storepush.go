// Package storepush holds the rules of a lore-only store's push and
// compaction settings (owner decision 2026-11-09, "store push modes"):
// how writes reach the remote (push: now | batch | manual, push_every)
// and whether old history is squashed (compact: off | on, compact_after).
// Layer L1: plain values and decisions, no file, clock or git access.
//
//	"push=batch push_every=10m" ──▶ ParseAssignments ──▶ Settings.Apply ──▶ Validate
//	last push, now, push_every  ──▶ BatchDue
//	last compaction, now        ──▶ AutoCompactDue (at most once a day)
//	local epoch, remote epoch   ──▶ Refetch (a compacted remote is re-fetched, never merged)
//
// Where the values live (wire): the personal store's in the `personal:`
// block of ~/.lore/config.yaml, an org store's in the store's own
// lore-store.yaml (shared by every clone, written by an org admin).
package storepush

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Mode is how a store's writes reach its remote.
type Mode string

// Push modes.
const (
	// ModeNow pushes each write at once (the default; lore before 0.1.18).
	ModeNow Mode = "now"
	// ModeBatch keeps writes local and pushes them as ONE commit at most
	// once per push_every, and always on `lore sync`.
	ModeBatch Mode = "batch"
	// ModeManual pushes only on `lore sync`.
	ModeManual Mode = "manual"
)

// Modes lists every push mode in the order help text shows them.
func Modes() []Mode { return []Mode{ModeNow, ModeBatch, ModeManual} }

// Compact switch values (the file and CLI spelling).
const (
	CompactOff = "off"
	CompactOn  = "on"
)

// Setting keys (`lore store set <store> key=value`, config.yaml and
// lore-store.yaml keys).
const (
	KeyPush         = "push"
	KeyPushEvery    = "push_every"
	KeyCompact      = "compact"
	KeyCompactAfter = "compact_after"
)

// Keys lists every setting key.
func Keys() []string { return []string{KeyPush, KeyPushEvery, KeyCompact, KeyCompactAfter} }

// Defaults and bounds.
const (
	// DefaultPushEvery is batch mode's push interval when none is set.
	DefaultPushEvery = 5 * time.Minute
	// MinPushEvery is the shortest push_every: a shorter one is mode now.
	MinPushEvery = time.Minute
	// MinCompactAfter is the youngest history compaction may squash: a
	// clone that was offline for a day can still replay onto the result.
	MinCompactAfter = Day
	// AutoCompactInterval is the least time between two automatic
	// compactions of one store.
	AutoCompactInterval = Day
	// Day is 24 hours (the `d` suffix of an age).
	Day = 24 * time.Hour
)

// ErrNotPositive is ParseAge's refusal of a zero or negative age; a
// setting with a minimum names the minimum instead (Apply).
var ErrNotPositive = errors.New("must be longer than zero")

// daySuffix marks an age written in whole days ("30d").
const daySuffix = "d"

// Settings are one store's push and compaction settings.
type Settings struct {
	Push      Mode
	PushEvery time.Duration
	Compact   bool
	// CompactAfter is the age past which history is squashed (0 = unset).
	CompactAfter time.Duration
}

// Default is a store with no settings: push now, compaction off.
func Default() Settings { return Settings{Push: ModeNow, PushEvery: DefaultPushEvery} }

// Raw is the settings as spelled in a file (each "" when unset).
type Raw struct {
	Push         string
	PushEvery    string
	Compact      string
	CompactAfter string
}

// IsZero reports whether no key is set.
func (r Raw) IsZero() bool { return r == Raw{} }

// FromRaw reads file values over the defaults; every bad value is named.
func FromRaw(r Raw) (Settings, error) {
	s := Default()
	var all []error
	for _, kv := range [][2]string{{KeyPush, r.Push}, {KeyPushEvery, r.PushEvery}, {KeyCompact, r.Compact}, {KeyCompactAfter, r.CompactAfter}} {
		if strings.TrimSpace(kv[1]) == "" {
			continue
		}
		next, err := s.Apply(kv[0], kv[1])
		if err != nil {
			all = append(all, err)
			continue
		}
		s = next
	}
	if err := errors.Join(all...); err != nil {
		return Default(), err
	}
	if err := s.Validate(); err != nil {
		return Default(), err
	}
	return s, nil
}

// Problems lists every problem of file values, one error each (config
// check names them all; FromRaw joins them).
func Problems(r Raw) []error {
	_, err := FromRaw(r)
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var out []error
		for _, e := range j.Unwrap() {
			if jj, ok := e.(interface{ Unwrap() []error }); ok {
				out = append(out, jj.Unwrap()...)
				continue
			}
			out = append(out, e)
		}
		return out
	}
	return []error{err}
}

// Raw is s as file values; defaults are left unset so a file only says
// what differs.
func (s Settings) Raw() Raw {
	var r Raw
	if s.Push != "" && s.Push != ModeNow {
		r.Push = string(s.Push)
	}
	if s.PushEvery != 0 && s.PushEvery != DefaultPushEvery {
		r.PushEvery = FormatAge(s.PushEvery)
	}
	if s.Compact {
		r.Compact = CompactOn
	}
	if s.CompactAfter != 0 {
		r.CompactAfter = FormatAge(s.CompactAfter)
	}
	return r
}

// Assignment is one key=value of `lore store set`.
type Assignment struct {
	Key   string
	Value string
}

// ParseAssignments splits key=value words; an empty list, a word without
// '=', an unknown key or a key given twice is refused naming the word.
func ParseAssignments(words []string) ([]Assignment, error) {
	if len(words) == 0 {
		return nil, fmt.Errorf("no setting given; use key=value with a key of %s", strings.Join(Keys(), ", "))
	}
	seen := map[string]bool{}
	out := make([]Assignment, 0, len(words))
	for _, w := range words {
		k, v, ok := strings.Cut(strings.TrimSpace(w), "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case !ok:
			return nil, fmt.Errorf("%q is not key=value (e.g. push=batch)", w)
		case !slices.Contains(Keys(), k):
			return nil, fmt.Errorf("unknown setting %q; the keys are %s", k, strings.Join(Keys(), ", "))
		case v == "":
			return nil, fmt.Errorf("%s has no value", k)
		case seen[k]:
			return nil, fmt.Errorf("%s is given twice", k)
		}
		seen[k] = true
		out = append(out, Assignment{Key: k, Value: v})
	}
	return out, nil
}

// Apply returns s with key set to value (not yet validated as a whole).
func (s Settings) Apply(key, value string) (Settings, error) {
	value = strings.TrimSpace(value)
	switch key {
	case KeyPush:
		m, err := ParseMode(value)
		if err != nil {
			return s, err
		}
		s.Push = m
	case KeyPushEvery:
		d, err := ParseAge(value)
		if errors.Is(err, ErrNotPositive) {
			return s, fmt.Errorf("push_every %s: must be at least %s", value, FormatAge(MinPushEvery))
		}
		if err != nil {
			return s, fmt.Errorf("push_every: %w", err)
		}
		s.PushEvery = d
	case KeyCompact:
		on, err := ParseCompact(value)
		if err != nil {
			return s, err
		}
		s.Compact = on
	case KeyCompactAfter:
		d, err := ParseAge(value)
		if errors.Is(err, ErrNotPositive) {
			return s, fmt.Errorf("compact_after %s: must be at least %s", value, FormatAge(MinCompactAfter))
		}
		if err != nil {
			return s, fmt.Errorf("compact_after: %w", err)
		}
		s.CompactAfter = d
	default:
		return s, fmt.Errorf("unknown setting %q; the keys are %s", key, strings.Join(Keys(), ", "))
	}
	return s, nil
}

// Validate checks the settings as a whole.
func (s Settings) Validate() error {
	var all []error
	if !slices.Contains(Modes(), s.Push) {
		all = append(all, fmt.Errorf("push %q: use now, batch or manual", s.Push))
	}
	if s.PushEvery < MinPushEvery {
		all = append(all, fmt.Errorf("push_every %s: must be at least %s", FormatAge(s.PushEvery), FormatAge(MinPushEvery)))
	}
	if s.CompactAfter != 0 && s.CompactAfter < MinCompactAfter {
		all = append(all, fmt.Errorf("compact_after %s: must be at least %s", FormatAge(s.CompactAfter), FormatAge(MinCompactAfter)))
	}
	if s.Compact && s.CompactAfter == 0 {
		all = append(all, errors.New("compact=on needs compact_after (e.g. compact_after=30d)"))
	}
	return errors.Join(all...)
}

// ParseMode reads a push mode.
func ParseMode(v string) (Mode, error) {
	m := Mode(strings.ToLower(strings.TrimSpace(v)))
	if !slices.Contains(Modes(), m) {
		return "", fmt.Errorf("push %q: use now, batch or manual", v)
	}
	return m, nil
}

// ParseCompact reads the compact switch.
func ParseCompact(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case CompactOn:
		return true, nil
	case CompactOff:
		return false, nil
	}
	return false, fmt.Errorf("compact %q: use on or off", v)
}

// ParseAge reads an age or interval: whole days ("30d") or Go syntax
// ("10m", "36h").
func ParseAge(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	var d time.Duration
	if n, ok := strings.CutSuffix(v, daySuffix); ok {
		days, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not a duration (use whole days such as 30d, or 10m, 36h)", v)
		}
		d = time.Duration(days) * Day
	} else {
		var err error
		if d, err = time.ParseDuration(v); err != nil {
			return 0, fmt.Errorf("%q is not a duration (use whole days such as 30d, or 10m, 36h)", v)
		}
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q %w", v, ErrNotPositive)
	}
	return d, nil
}

// FormatAge spells d the way ParseAge reads it: whole days as "30d",
// anything else in Go syntax without a zero tail ("10m", "1h30m").
func FormatAge(d time.Duration) string {
	if d > 0 && d%Day == 0 {
		return strconv.FormatInt(int64(d/Day), 10) + daySuffix
	}
	s := d.String() // 1h30m0s, 10m0s, 45s
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// BatchDue reports whether batch mode pushes now: the oldest write still
// waiting is push_every or more old. Nothing waiting (zero) is never due.
// A write dated in the future (a clock set back) is due, so a wrong clock
// never holds writes back for good. lore runs no daemon or timer: a
// command that runs anyway asks this and pushes inside itself.
func BatchDue(oldestWaiting, now time.Time, every time.Duration) bool {
	if oldestWaiting.IsZero() {
		return false
	}
	if oldestWaiting.After(now) {
		return true
	}
	return now.Sub(oldestWaiting) >= every
}

// StaleAfter is how old a waiting batch is before `lore doctor` warns
// (nothing ran lore to push it, or every push failed).
const StaleAfter = Day

// Stale reports whether the oldest waiting write is older than StaleAfter.
func Stale(oldestWaiting, now time.Time) bool {
	return !oldestWaiting.IsZero() && now.Sub(oldestWaiting) > StaleAfter
}

// AutoCompactDue reports whether the automatic compaction may run: never
// ran, or ran AutoCompactInterval or more ago (a stamp in the future is
// due for the same reason as BatchDue).
func AutoCompactDue(lastCompact, now time.Time) bool {
	if lastCompact.IsZero() || lastCompact.After(now) {
		return true
	}
	return now.Sub(lastCompact) >= AutoCompactInterval
}

// Refetch reports whether a clone must re-fetch instead of merging: the
// remote's epoch is higher than the one the clone's history carries, so
// the remote history was compacted (rewritten) since the clone last
// merged it.
func Refetch(localEpoch, remoteEpoch int) bool { return remoteEpoch > localEpoch }

// Cutoff is the time before which history is squashed.
func (s Settings) Cutoff(now time.Time) time.Time { return now.Add(-s.CompactAfter) }

// Describe is the one-line summary `lore store show`, `lore doctor` and
// `lore sync status` print ("push batch every 10m, compact on after 30d").
func (s Settings) Describe() string {
	out := "push " + string(s.Push)
	if s.Push == ModeBatch {
		out += " every " + FormatAge(s.PushEvery)
	}
	if s.Compact {
		out += ", compact on after " + FormatAge(s.CompactAfter)
	} else {
		out += ", compact off"
	}
	return out
}
