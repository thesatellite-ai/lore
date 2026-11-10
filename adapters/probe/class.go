package probe

import (
	"regexp"
	"strings"
	"time"
)

// Class is what a probe found, the input the check layer (ops/setup, wire)
// maps to a check status (ONB 2.2: ok / warn / fail / skip / error).
type Class string

// Probe classes. Each probe documents which it returns.
const (
	// ClassOK: the probe ran and the thing holds (read works, push allowed,
	// ssh authenticated, gh answered).
	ClassOK Class = "ok"
	// ClassNoAccess: the remote says "not found" or refuses the read; GitHub
	// does not tell a missing repo from a hidden one (ONB 4.4 store.read
	// "not found / no access").
	ClassNoAccess Class = "no_access"
	// ClassNoWrite: the remote refused the dry-run push (ONB 4.4 store.write).
	ClassNoWrite Class = "no_write"
	// ClassAuthDenied: ssh "Permission denied (publickey)" or an https auth
	// failure: the key or login was refused (ONB 4.3 github.ssh_auth).
	ClassAuthDenied Class = "auth_denied"
	// ClassHostKey: ssh refused the host key (unknown or changed) in batch
	// mode; probes never write known_hosts.
	ClassHostKey Class = "host_key"
	// ClassNotLoggedIn: gh has no login (or the login was rejected).
	ClassNotLoggedIn Class = "not_logged_in"
	// ClassUnreachable: DNS, connection refused, no route (ONB 4.4 "host
	// unreachable": network, not auth).
	ClassUnreachable Class = "unreachable"
	// ClassTimeout: the probe outlived its timeout (or the wait for a
	// limiter slot did); the check status is "error".
	ClassTimeout Class = "timeout"
	// ClassToolMissing: the program (git, ssh, gh, ssh-keygen) is not on
	// PATH; the check status is "error" (or "warn" for optional gh).
	ClassToolMissing Class = "tool_missing"
	// ClassBadInput: the probe refused its input before running anything (a
	// url with user:token@, an alias with odd characters, a non-.pub path).
	ClassBadInput Class = "bad_input"
	// ClassFailed: the program ran and failed in a way no rule above names;
	// Detail carries one sanitised line.
	ClassFailed Class = "failed"
)

// Ran reports whether the probe itself ran to an answer (the check maps
// the other classes to the "error" status: timeout, tool missing, bad input).
func (c Class) Ran() bool {
	switch c {
	case ClassTimeout, ClassToolMissing, ClassBadInput:
		return false
	}
	return true
}

// Outcome is the part every probe result shares.
type Outcome struct {
	Class Class `json:"class"`
	// Detail is one sanitised line: the program's own message with any
	// user:token@ and token-shaped string removed, or the probe's reason.
	Detail string        `json:"detail,omitempty"`
	Took   time.Duration `json:"took"`
}

// Stderr phrases, lower case, matched in classify's order. Real messages:
//
//	GitHub ssh, read-only:  ERROR: Permission to o/r.git denied to mira.
//	GitHub https, read-only: remote: Permission to o/r.git denied to mira. … error: 403
//	GitLab:                 You are not allowed to push code to this project.
//	missing / hidden:       ERROR: Repository not found. | does not appear to be a git repository
//	key refused:            git@github.com: Permission denied (publickey).
var (
	authDeniedWords  = []string{"permission denied (publickey", "permission denied (keyboard-interactive", "authentication failed", "could not read username", "returned error: 401", "invalid username or password"}
	noWriteWords     = []string{"denied to ", "not allowed to push", "you can't push", "returned error: 403", "write access to repository not granted", "permission to push"}
	noAccessWords    = []string{"repository not found", "does not appear to be a git repository", "could not be found", "returned error: 404", "not found"}
	unreachableWords = []string{"could not resolve host", "could not resolve hostname", "temporary failure in name resolution", "name or service not known", "nodename nor servname", "connection refused", "connection timed out", "operation timed out", "network is unreachable", "no route to host", "failed to connect", "connection reset", "connection closed by"}
	hostKeyWords     = []string{"host key verification failed", "remote host identification has changed"}
)

// classifyRemote maps a failed git/ssh run's stderr to a class. Order
// matters: "Permission denied (publickey)" is an auth failure, not a
// missing write right; "denied to <user>" is a write refusal even though
// the same stderr also says "Could not read from remote repository".
func classifyRemote(stderr string) Class {
	low := strings.ToLower(stderr)
	switch {
	case containsAny(low, hostKeyWords):
		return ClassHostKey
	case containsAny(low, authDeniedWords):
		return ClassAuthDenied
	case containsAny(low, noWriteWords):
		return ClassNoWrite
	case containsAny(low, noAccessWords):
		return ClassNoAccess
	case containsAny(low, unreachableWords):
		return ClassUnreachable
	}
	return ClassFailed
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// failure builds the Outcome of a run that did not exit 0: timeout, tool
// missing, or classify(stderr) with a sanitised detail line.
func failure(r execResult, bin string, classify func(string) Class) Outcome {
	switch {
	case r.timedOut:
		return Outcome{Class: ClassTimeout, Detail: bin + " timed out"}
	case r.missing:
		return Outcome{Class: ClassToolMissing, Detail: bin + " not found on PATH"}
	case r.startErr:
		return Outcome{Class: ClassFailed, Detail: bin + " could not start"}
	}
	return Outcome{Class: classify(r.stderr), Detail: Sanitize(firstLine(r.stderr))}
}

// firstLine returns the most telling line of a program's stderr: the first
// one naming an error, else the first non-empty one.
func firstLine(s string) string {
	var first string
	for l := range strings.SplitSeq(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if first == "" {
			first = l
		}
		low := strings.ToLower(l)
		if strings.HasPrefix(low, "error") || strings.HasPrefix(low, "fatal") || strings.HasPrefix(low, "remote: permission") || strings.Contains(low, "permission denied") {
			return l
		}
	}
	return first
}

// Credential shapes Sanitize removes from any text a result carries.
var (
	// the userinfo part (user, optional password) before the host of a url with a scheme.
	// The pattern is joined from two parts so the repo's own leak scanner,
	// which reads source lines, does not take the pattern for a credential.
	reURLUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*:` + `//)[^/@\s'"]+@`)
	// well-known token prefixes (GitHub, GitLab, Slack, AWS, npm, OpenAI-style).
	reTokenShape = regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{16,}|xox[bpas]-[A-Za-z0-9-]{10,}|AKIA[A-Z0-9]{16}|npm_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9_-]{20,})`)
)

// tokenMask replaces a token-shaped string.
const tokenMask = "<redacted>"

// Sanitize removes user:token@ (any userinfo of a scheme url) and
// token-shaped strings from s and trims it to one short line. Every Detail
// and every url a result carries goes through it.
func Sanitize(s string) string { return SanitizeWithin(s, maxDetail) }

// MaxCheckDetail caps a check result's Detail (wire's Probe). A check's
// detail is a sentence lore composes from facts (the key, the alias, who
// each logs in as, which aliases did not answer yet), longer than the
// one stderr line maxDetail is sized for; at 200 bytes github.key_matches
// lost its last clause. Still one line.
const MaxCheckDetail = 500

// SanitizeWithin is Sanitize with a byte limit of its own (never cutting
// a character in half).
func SanitizeWithin(s string, limit int) string {
	s = reURLUserinfo.ReplaceAllString(s, "$1")
	s = reTokenShape.ReplaceAllString(s, tokenMask)
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > limit {
		s = strings.ToValidUTF8(s[:limit], "") + "…"
	}
	return s
}
