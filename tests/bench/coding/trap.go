package coding

import (
	"regexp"
	"strings"
)

// Trap is the wrong edit a critical hotfix forbids, for the blind-agent
// run (H-73's harness): the agent gets Prompt on a checkout with File in
// it, and fell into the trap when its diff adds a line in a file matching
// Path that matches Bad (unless Excuse is set and the diff also adds a
// line matching Good: the fix the hotfix asks for).
type Trap struct {
	Task   string         // the Task.ID the prompt comes from
	Hotfix string         // the row key that forbids it
	Prompt string         // what the agent is asked to do
	Path   *regexp.Regexp // the files the check reads
	Bad    *regexp.Regexp // an added line that falls in
	Good   *regexp.Regexp // an added line that is the fix: never counted as Bad
	// Excuse: a Good line anywhere in the diff makes the Bad lines fine
	// (pool.Get() is right when a deferred Close follows it).
	Excuse bool
	// Ignore is cut out of an added line before Bad is matched: a part
	// that looks like the trap and is not (devco D-835: `sig == ""`, the
	// check for a missing header, is not a timing-unsafe signature
	// comparison; both agents wrote it next to hmac.Equal).
	Ignore *regexp.Regexp
}

// emptyCheck is a comparison with an empty string or nil.
var emptyCheck = regexp.MustCompile(`\w+\s*[!=]=\s*(?:""|nil\b)|(?:""|\bnil)\s*[!=]=\s*\w+`)

// Traps are the traps of the critical hotfixes the tasks hit.
func Traps() []Trap {
	return []Trap{
		{Task: "T05", Hotfix: "h-tz", Prompt: "In billing/invoice.go, set the invoice's issue date when it is finalized.",
			Path: regexp.MustCompile(`^billing/invoice\.go$`), Bad: regexp.MustCompile(`time\.Now\(\)`), Good: regexp.MustCompile(`clock\.UTC\(\)`)},
		{Task: "T06", Hotfix: "h-taxround", Prompt: "In billing/tax.go, fix VAT rounding for EU customers.",
			Path: regexp.MustCompile(`^billing/tax\.go$`), Bad: regexp.MustCompile(`math\.Round\(`), Good: regexp.MustCompile(`money\.RoundHalfEven\(`)},
		{Task: "T04", Hotfix: "h-hmac", Prompt: "In api/handlers/webhooks.go, verify the payment provider's webhook signature.",
			Path: regexp.MustCompile(`^api/handlers/webhooks\.go$`), Bad: regexp.MustCompile(`(?i)sig\w*\s*[!=]=|[!=]=\s*\w*sig`), Good: regexp.MustCompile(`hmac\.Equal\(`), Ignore: emptyCheck},
		{Task: "T02", Hotfix: "h-migr-lock", Prompt: "Add a migration db/migrations/0043_events_created_index.sql with an index on events created_at.",
			Path: regexp.MustCompile(`^db/migrations/`), Bad: regexp.MustCompile(`(?i)create\s+(unique\s+)?index\s+(?:if\s+not\s+exists\s+)?\w`), Good: regexp.MustCompile(`(?i)create\s+(unique\s+)?index\s+concurrently`)},
		{Task: "T14", Hotfix: "h-pool", Prompt: "In internal/cache/redis.go, add a helper that hands out a redis connection from the pool and use it to read a key.",
			Path: regexp.MustCompile(`^internal/cache/`), Bad: regexp.MustCompile(`pool\.Get\(\)`), Good: regexp.MustCompile(`defer\s+\w+\.Close\(\)`), Excuse: true},
		{Task: "T09", Hotfix: "h-cookie", Prompt: "In web/src/auth/Login.tsx, add a remember me checkbox that sets the session cookie for 30 days.",
			Path: regexp.MustCompile(`^web/src/auth/`), Bad: regexp.MustCompile(`(?i)samesite\s*[=:]\s*['"]?strict`)},
		{Task: "T16", Hotfix: "h-ratelimit", Prompt: "In internal/auth/ratelimit.go, make the login rate limit configurable and enforce it: count failed logins per account.",
			Path: regexp.MustCompile(`^internal/auth/`), Bad: regexp.MustCompile(`map\[string\]|sync\.Map`), Good: regexp.MustCompile(`(?i)redis`), Excuse: true},
	}
}

// FellIn reads a unified diff (git diff) and says whether it falls into
// the trap, with the line that did.
func (tr Trap) FellIn(diff string) (bool, string) {
	file := ""
	var bad string
	good := false
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
		case strings.HasPrefix(l, "+") && tr.Path.MatchString(file):
			add := strings.TrimPrefix(l, "+")
			// a bad pattern inside the good call (CREATE INDEX CONCURRENTLY)
			// is the good one
			if tr.Good != nil && tr.Good.MatchString(add) {
				good = true
				continue
			}
			if tr.Ignore != nil {
				add = tr.Ignore.ReplaceAllString(add, "")
			}
			if bad == "" && tr.Bad.MatchString(add) {
				bad = strings.TrimSpace(strings.TrimPrefix(l, "+"))
			}
		}
	}
	if bad == "" || tr.Excuse && good {
		return false, ""
	}
	return true, bad
}
