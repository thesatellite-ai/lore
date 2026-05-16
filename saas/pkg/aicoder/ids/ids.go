package ids

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// idPattern enforces the <3-char-prefix>_<32-char-hex> format on input.
// Prefix is lowercase ASCII letters only; tail is lowercase hex.
var idPattern = regexp.MustCompile(`^[a-z]{3}_[0-9a-f]{32}$`)

// New returns a new opaque ID with the given prefix.
//
// The ID embeds a UUIDv7 (48-bit ms timestamp + 74 random bits + version/variant).
// Lexicographic sort on the resulting string is chronologically ordered to
// millisecond granularity (Round 34.4: NOT strict sequence within a ms — pair
// with created_at as ORDER BY tiebreaker).
//
// Returns an error if UUIDv7 generation fails (rare; usually only on systems
// without a working RNG).
func New(prefix string) (string, error) {
	if err := validatePrefix(prefix); err != nil {
		return "", err
	}
	u, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("ids: NewV7: %w", err)
	}
	return prefix + "_" + strings.ReplaceAll(u.String(), "-", ""), nil
}

// MustNew is like New but panics on error. Use only in initialization paths
// where failure means the host RNG is broken — i.e., we want a hard crash.
func MustNew(prefix string) string {
	id, err := New(prefix)
	if err != nil {
		panic(err)
	}
	return id
}

// Validate checks the format of an ID and ensures the prefix matches the
// expected one. Returns nil if valid.
//
// Rejects:
//   - empty / whitespace
//   - wrong length (must be exactly 36 chars: 3 + 1 + 32)
//   - missing/wrong separator
//   - prefix mismatch
//   - non-hex tail
//   - non-UUIDv7 versions (e.g., a v4 UUID would parse but fail version check)
func Validate(id, expectedPrefix string) error {
	if id == "" {
		return fmt.Errorf("ids: empty")
	}
	if !idPattern.MatchString(id) {
		return fmt.Errorf("ids: invalid format: %q (expected <prefix>_<32-hex>)", id)
	}
	if !strings.HasPrefix(id, expectedPrefix+"_") {
		return fmt.Errorf("ids: expected prefix %q, got %q", expectedPrefix, id[:3])
	}
	if _, err := parseUUID(id); err != nil {
		return err
	}
	return nil
}

// ValidateAny checks the format and confirms the prefix is one of the
// registered prefixes (any entity kind). Useful for cross-table lookups
// where the caller doesn't know which entity an opaque ID belongs to.
func ValidateAny(id string) error {
	if id == "" {
		return fmt.Errorf("ids: empty")
	}
	if !idPattern.MatchString(id) {
		return fmt.Errorf("ids: invalid format: %q", id)
	}
	prefix := id[:3]
	for _, p := range AllPrefixes {
		if p == prefix {
			if _, err := parseUUID(id); err != nil {
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("ids: unregistered prefix %q", prefix)
}

// PrefixOf returns the 3-char prefix portion of an ID without full validation.
// Returns empty string if the ID is too short.
func PrefixOf(id string) string {
	if len(id) < 3 {
		return ""
	}
	return id[:3]
}

// IDToTime extracts the embedded UUIDv7 timestamp.
//
// Per Round 34.3, the correct google/uuid API is:
//
//	sec, nsec := u.Time().UnixTime()  // returns (int64, int64)
//	return time.Unix(sec, nsec), nil
//
// Returns an error if the ID format is invalid OR the version is not 7.
func IDToTime(id string) (time.Time, error) {
	u, err := parseUUID(id)
	if err != nil {
		return time.Time{}, err
	}
	sec, nsec := u.Time().UnixTime()
	return time.Unix(sec, nsec), nil
}

// parseUUID reconstitutes a uuid.UUID from a mini ID's hex tail.
// google/uuid expects dashed form (8-4-4-4-12); we re-insert the dashes.
func parseUUID(id string) (uuid.UUID, error) {
	if !idPattern.MatchString(id) {
		return uuid.Nil, fmt.Errorf("ids: invalid format: %q", id)
	}
	hex := id[4:] // strip "xxx_"
	dashed := hex[0:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:32]
	u, err := uuid.Parse(dashed)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ids: parse: %w", err)
	}
	if u.Version() != 7 {
		return uuid.Nil, fmt.Errorf("ids: not UUIDv7 (got version %d)", u.Version())
	}
	return u, nil
}

// validatePrefix checks the prefix string is exactly 3 lowercase ASCII letters.
func validatePrefix(prefix string) error {
	if len(prefix) != 3 {
		return fmt.Errorf("ids: prefix must be 3 chars, got %d", len(prefix))
	}
	for _, c := range prefix {
		if c < 'a' || c > 'z' {
			return fmt.Errorf("ids: prefix must be lowercase a-z only: %q", prefix)
		}
	}
	return nil
}
