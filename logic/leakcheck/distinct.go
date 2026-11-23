package leakcheck

// distinct.go — which metadata values the exact-token rule matches on
// their own (#138, devco:D-916).
//
// Why: the exact-token rule refused a client repo's commit because a code
// comment held a plain date (2026-11-22) that equalled a fact_verified_at
// value of a row in another store. Every key first registered in a store
// starts internal and auto-registered keys are typed "text", so any value
// of MinTokenLen characters became a leak token: dates, numbers, versions
// and status words collide with ordinary text by coincidence, and today's
// date collides most of all. Only a distinctive value can name an internal
// detail when it appears alone: a row id, a host, an IP address, an email,
// a URL, a value mixing letters and digits, or a long string. Every other
// value is left to the key-pair rule, which still sees "fact_verified_at
// 2026-11-22".

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MinLongTokenLen is the length from which a value counts as distinctive
// whatever its characters (a long phrase or name rarely appears by
// coincidence; a single common word of this length is rare).
const MinLongTokenLen = 24

// commonTypes are the key types whose values are never distinctive on
// their own: a date, a number or a flag says nothing without its key.
var commonTypes = map[string]bool{"number": true, "bool": true, "date": true, "datetime": true, "duration": true}

// addressTypes are the key types whose values always are (logic/meta's
// type words for hosts and addresses).
var addressTypes = map[string]bool{"dns": true, "ip": true, "cidr": true, "url": true, "email": true}

// Shapes of common values, matched against the lowercased value.
var (
	// 2026-11-22, 2026/11/22, 2026.11.22, 2026-11, 22.11.2026, 11/22/26,
	// optionally with a time and zone (2026-11-22t10:00:00z, … 10:00+05:30).
	dateRe = regexp.MustCompile(`^(\d{4}[-/.]\d{1,2}([-/.]\d{1,2})?|\d{1,2}[-/.]\d{1,2}[-/.]\d{2,4})([t ]\d{1,2}:\d{2}(:\d{2}(\.\d+)?)?)?( ?(z|utc|[+-]\d{2}:?\d{2}))?$`)
	// 10:00, 10:00:00, 10:00:00.5z
	timeRe = regexp.MustCompile(`^\d{1,2}:\d{2}(:\d{2}(\.\d+)?)?( ?(z|utc|am|pm|[+-]\d{2}:?\d{2}))?$`)
	// 22 november 2026, nov 22, 2026, 22-nov-2026, november 2026
	monthDateRe = regexp.MustCompile(`^(\d{1,2}(st|nd|rd|th)?[ -])?(jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.?([ -]\d{1,2}(st|nd|rd|th)?)?,?[ -]\d{2,4}$`)
	// q4-2026, 2026-q4, 2026q4, h1 2026, fy2026, fy26
	periodRe = regexp.MustCompile(`^(([qh][1-4][ -]?\d{2,4})|(\d{4}[ -]?[qh][1-4])|(fy[ -]?\d{2,4}))$`)
	// v1.2.3, 1.2.3-rc1, v0.1.38 (a dotted IP is checked before this)
	versionRe = regexp.MustCompile(`^v?\d+(\.\d+)+([-+][a-z0-9.]+)?$`)
	// a number with a unit: 512mb, 10ms, 1080p, 35%, 2.5k
	unitRe = regexp.MustCompile(`^[-+]?\d+([.,]\d+)?\s?[a-z%]{1,3}$`)
	// ISO 8601 durations: p1y2m, pt1h30m
	isoDurationRe = regexp.MustCompile(`^p(\d+[ymwd])*(t(\d+(\.\d+)?[hms])+)?$`)
	// a row id: prefix_hex
	rowIDRe      = regexp.MustCompile(`^[a-z]{3}_[0-9a-f]{8,}$`)
	emailValueRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[a-z]{2,}$`)
	// a dotted host name of letters, digits and hyphens
	hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
)

// boolWords are the flag and status-like words a value may be that are
// never distinctive whatever their length (letters-only values under
// MinLongTokenLen already are not; these keep a list explicit).
var boolWords = map[string]bool{"true": true, "false": true, "yes": true, "no": true, "on": true, "off": true, "enabled": true, "disabled": true}

// DistinctiveToken reports whether a metadata value of key type typ may
// be matched by the exact-token rule on its own: long enough, not a
// common value (date, datetime, time, number, flag, version, duration, a
// period such as q4-2026, a status word), and either an address (row id,
// host, IP, email, URL), a value mixing letters and digits, or at least
// MinLongTokenLen characters. A key typed number, bool, date, datetime or
// duration never is; one typed dns, ip, cidr, url or email always is when
// long enough. v is compared lowercased and trimmed.
func DistinctiveToken(v, typ string) bool {
	s := strings.ToLower(strings.TrimSpace(v))
	if utf8.RuneCountInString(s) < MinTokenLen {
		return false
	}
	elem := strings.TrimSuffix(strings.TrimPrefix(typ, "list<"), ">")
	if commonTypes[elem] {
		return false
	}
	if addressTypes[elem] || addressValue(s) {
		return true
	}
	if commonValue(s) {
		return false
	}
	if utf8.RuneCountInString(s) >= MinLongTokenLen {
		return true
	}
	return hasLetterAndDigit(s)
}

// addressValue reports a value shaped as a row id, IP address, email,
// URL or a host name with three or more labels or a digit or hyphen (a
// two-label word pair such as node.js or main.go is no host on its own).
func addressValue(s string) bool {
	if rowIDRe.MatchString(s) || emailValueRe.MatchString(s) || strings.Contains(s, "://") {
		return true
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	if _, err := netip.ParsePrefix(s); err == nil {
		return true
	}
	if versionRe.MatchString(s) || !hostRe.MatchString(s) {
		return false // 1.2.3-rc1 is a version, not a host
	}
	labels := strings.Split(s, ".")
	last := labels[len(labels)-1]
	if !strings.ContainsFunc(last, unicode.IsLetter) {
		return false // 1.2.3 or a version: not a host
	}
	return len(labels) >= 3 || strings.ContainsAny(s, "-0123456789")
}

// commonValue reports a date, datetime, time, number, flag, version,
// duration or period: values ordinary text holds by coincidence.
func commonValue(s string) bool {
	if boolWords[s] {
		return true
	}
	if _, err := strconv.ParseFloat(strings.NewReplacer("_", "", ",", "").Replace(s), 64); err == nil {
		return true
	}
	if _, err := time.ParseDuration(s); err == nil {
		return true
	}
	for _, re := range []*regexp.Regexp{dateRe, timeRe, monthDateRe, periodRe, versionRe, unitRe, isoDurationRe} {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// hasLetterAndDigit reports a value holding at least one letter and one
// digit (zq81xk, an internal code): such a value is rarely ordinary text.
func hasLetterAndDigit(s string) bool {
	return strings.ContainsFunc(s, unicode.IsLetter) && strings.ContainsFunc(s, unicode.IsDigit)
}
