package teamprofile

import (
	"net/url"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// Normalisation constants (TEAM_PROFILES 6.1).
const (
	emailAt     = "@"
	domainDot   = "."
	handleMark  = "@"
	phonePlus   = '+'
	trailSlash  = "/"
	schemeHTTP  = "http"
	schemeHTTPS = "https"
	// minPhoneDigits: shorter is a typo, not a phone number (6.1).
	minPhoneDigits = 7
)

// handleURLPrefixes are the profile URL prefixes a handle may be pasted
// with (6.1); matched case-insensitively and dropped.
var handleURLPrefixes = [...]string{
	"https://github.com/",
	"https://gitlab.com/",
	"https://x.com/",
	"https://www.linkedin.com/in/",
}

// Normalize returns the value_norm of value for contact kind kind
// (TEAM_PROFILES 6.1): the form identity matching and the natural id
// compare, so "Mira.Patel@Gmail.com" and "mira.patel@gmail.com" are one
// contact. A value that fails the kind's check, or an unknown kind, is
// E_CONTACT_TYPE naming the check. Gmail dots and +tags are not folded:
// they are different addresses to git.
func Normalize(kind, value string) (string, error) {
	v := strings.TrimSpace(value)
	switch kind {
	case schema.ContactKindEmail:
		return normEmail(v)
	case schema.ContactKindHandle:
		return normHandle(v)
	case schema.ContactKindPhone:
		return normPhone(v)
	case schema.ContactKindURL:
		return normURL(v)
	case schema.ContactKindText:
		if v == "" {
			return "", contactErr("a text contact is not empty")
		}
		return v, nil
	}
	return "", contactErr("contact kind " + kind + " is not one of " + strings.Join(schema.ContactKinds(), ", "))
}

// normEmail: lower-case; exactly one @, a local part, a dot inside the
// domain, no spaces.
func normEmail(v string) (string, error) {
	v = strings.ToLower(v)
	local, domain, ok := strings.Cut(v, emailAt)
	if !ok || local == "" || strings.Contains(domain, emailAt) || strings.ContainsAny(v, " \t") ||
		!strings.Contains(domain, domainDot) || strings.HasPrefix(domain, domainDot) || strings.HasSuffix(domain, domainDot) {
		return "", contactErr("an email holds exactly one @ and a dot in the domain (mira@example.com)")
	}
	return v, nil
}

// normHandle: a known profile URL prefix and a leading @ dropped, a
// trailing / dropped, lower-cased. What is left must be one word: a / or a
// space left over means a URL lore does not know, not a handle.
func normHandle(v string) (string, error) {
	for _, p := range handleURLPrefixes {
		if len(v) >= len(p) && strings.EqualFold(v[:len(p)], p) {
			v = v[len(p):]
			break
		}
	}
	v = strings.TrimPrefix(v, handleMark)
	v = strings.TrimSuffix(v, trailSlash)
	v = strings.ToLower(v)
	if v == "" || strings.ContainsAny(v, " \t/") {
		return "", contactErr("a handle is one word (mirap, @mirap or https://github.com/mirap)")
	}
	return v, nil
}

// normPhone: a leading + and the digits; at least minPhoneDigits digits.
func normPhone(v string) (string, error) {
	var b strings.Builder
	if strings.HasPrefix(v, string(phonePlus)) {
		b.WriteRune(phonePlus)
	}
	digits := 0
	for _, r := range v {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
			digits++
		}
	}
	if digits < minPhoneDigits {
		return "", contactErr("a phone number holds at least 7 digits (+90 532 000 0000)")
	}
	return b.String(), nil
}

// normURL: http or https with a host; scheme and host lower-cased; a
// trailing / dropped. A URL with credentials is refused (E_URL_CREDENTIALS):
// a contact is shown plain (TP-6), so it must never carry a password.
func normURL(v string) (string, error) {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "", contactErr("a url starts with http:// or https:// and names a host")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS {
		return "", contactErr("a url starts with http:// or https:// and names a host")
	}
	if u.User != nil {
		return "", errs.New(errs.URLUserinfo, "a contact url holds credentials; give the address without user:password@")
	}
	u.Host = strings.ToLower(u.Host)
	return strings.TrimSuffix(u.String(), trailSlash), nil
}

// contactErr is E_CONTACT_TYPE with the check that failed.
func contactErr(check string) error {
	return errs.New(errs.ContactType, "contact value: "+check).WithHint("lore contact-type list shows each type's kind")
}
