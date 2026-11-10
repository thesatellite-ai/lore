package value

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/thesatellite-ai/lore/model/canon"
)

// KindSecretRef is a resource field holding the NAME of an entry in a
// secret manager, never the secret (RESOURCES 3.1/3.5 item 10): the same
// URI as the inside of a ${secret:…} reference, <scheme>://<locator>
// [?holder=<id>&label=<name>] (REFERENCE FORMAT, SQ1).
const KindSecretRef Kind = "secret_ref"

// Generic secret_ref checks of RESOURCES 3.5, for every manager. Which
// managers exist (E_REF_SCHEME) is decided above this package (logic/refs
// Managers, plugins later), so this type checks only the shape.
const (
	maxSecretRef = 200 // at most 200 characters
	// randomSegment: a locator segment longer than this that mixes upper
	// case, lower case and digits looks like a pasted password, not a name.
	randomSegment = 16
)

// schemeWord is a URI scheme as the managers name them (gopass, aws-secrets).
var schemeWord = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)

// SecretRef is a validated secret manager entry name.
type SecretRef struct{ s string }

// SecretRefOf validates s as manager://locator: at most 200 characters, no
// spaces or control characters, a scheme, a non-empty locator, and no
// segment that looks random (so a pasted password fails even when no
// credential pattern matches it). The error never repeats s.
func SecretRefOf(s string) (SecretRef, error) {
	if s == "" || len(s) > maxSecretRef {
		return SecretRef{}, invalid(KindSecretRef, "empty or longer than 200 characters")
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return SecretRef{}, invalid(KindSecretRef, "spaces or control characters")
		}
	}
	// url.Parse lower-cases the scheme, so the written one is checked raw.
	scheme, _, hasSep := strings.Cut(s, "://")
	u, err := url.Parse(s)
	if err != nil || !hasSep || !schemeWord.MatchString(scheme) || u.Opaque != "" || u.User != nil {
		return SecretRef{}, invalid(KindSecretRef, "want <manager>://<entry name>")
	}
	loc := u.Host + u.Path
	if strings.Trim(loc, "/") == "" {
		return SecretRef{}, invalid(KindSecretRef, "no entry name after the manager")
	}
	for _, seg := range strings.Split(loc, "/") {
		if looksRandom(seg) {
			return SecretRef{}, invalid(KindSecretRef, "a part looks like a secret, not an entry name")
		}
	}
	return SecretRef{s: s}, nil
}

// looksRandom: longer than randomSegment and mixing upper, lower and digits.
func looksRandom(seg string) bool {
	if len(seg) <= randomSegment {
		return false
	}
	var up, low, dig bool
	for _, r := range seg {
		switch {
		case unicode.IsUpper(r):
			up = true
		case unicode.IsLower(r):
			low = true
		case unicode.IsDigit(r):
			dig = true
		}
	}
	return up && low && dig
}

// Manager is the scheme (gopass, op, aws-secrets, …).
func (v SecretRef) Manager() string {
	m, _, _ := strings.Cut(v.s, "://")
	return m
}

// Kind implements Value.
func (SecretRef) Kind() Kind { return KindSecretRef }

// String returns the reference text.
func (v SecretRef) String() string { return v.s }

// MarshalJSON implements Value.
func (v SecretRef) MarshalJSON() ([]byte, error) { return canon.String(v.s), nil }
func (SecretRef) sealed()                        {}
