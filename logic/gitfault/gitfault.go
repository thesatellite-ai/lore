// Package gitfault turns the standard error of a failed git network call
// (fetch, push, ls-remote) into what a person can act on: which kind of
// failure it was and one whole, readable sentence of git's that says why.
//
// Why it exists: git ends almost every remote failure with the same
// advice ("Could not read from remote repository. / Please make sure you
// have the correct access rights / and the repository exists."). Code that
// showed the last line of stderr printed "and the repository exists." as
// the reason, and code that showed all of it printed five lines. And every
// remote failure was called "offline", so a mistyped or deleted remote
// looked like a network outage forever. One rule here, used by every place
// that shows git's reason:
//
//	stderr ─▶ Classify ─▶ Unreachable | Missing | Denied
//	       └▶ Reason   ─▶ the telling line, "fatal: " cut, advice dropped,
//	                      credentials masked (repourl.MaskText), never cut
//	                      mid-word
package gitfault

import (
	"regexp"
	"strings"

	"github.com/thesatellite-ai/lore/logic/repourl"
)

// Kind is what a failed git remote call means for the person.
type Kind string

// Kinds of Classify.
const (
	// Unreachable: no answer (no network, DNS, timeout, refused
	// connection, or nothing git could name). Waiting fixes it.
	Unreachable Kind = "unreachable"
	// Missing: the address answers but holds no git repository (a wrong
	// or deleted remote, a path that is not a repository). Waiting never
	// fixes it: the address must change.
	Missing Kind = "missing"
	// Unreadable: a local path that exists but this user may not read
	// (permissions). The address is right; the permissions must change
	// (B3-22: it was told to change a correct address).
	Unreadable Kind = "unreadable"
	// Denied: the remote refused this machine's credentials. The key,
	// token or account must change.
	Denied Kind = "denied"
)

// deniedText marks a refusal of credentials (lower case). It wins over a
// "not found" in the same stderr: hosting services answer "Repository not
// found" for a repository the key may not see.
var deniedText = []string{"permission denied", "authentication failed", "could not read username", "could not read password",
	"returned error: 401", "returned error: 403", "host key verification failed", "access denied"}

// unreachableText marks a remote that never answered (lower case).
var unreachableText = []string{"could not resolve host", "could not resolve hostname", "connection refused", "connection timed out",
	"operation timed out", "timed out", "network is unreachable", "no route to host", "connection reset", "temporary failure in name resolution",
	"failed to connect", "connection closed by remote host", "unable to look up"}

// missingText marks an address with no repository behind it (lower case).
var missingText = []string{"does not appear to be a git repository", "repository not found", "not a git repository"}

// missingHTTPS matches https's "repository '<url>' not found".
var missingHTTPS = regexp.MustCompile(`repository '[^']*' not found`)

// adviceLines are the lines git adds to every remote failure; they never
// say why (lower case, compared after the prefix is cut).
var adviceLines = []string{"could not read from remote repository.", "please make sure you have the correct access rights", "and the repository exists."}

// gitPrefixes are the labels git and remotes put before a message.
var gitPrefixes = []string{"fatal: ", "error: ", "ERROR: ", "remote: ", "warning: "}

// MaxReason bounds Reason; longer text ends at the last whole word before
// it, followed by "…".
const MaxReason = 300

// PathState is what the file system says of a local path a remote names
// (PathCheck's answer).
type PathState int

// Path states of PathCheck.
const (
	// PathUnknown: not checked, or nothing certain (the default).
	PathUnknown PathState = iota
	// PathAbsent: no such file or folder.
	PathAbsent
	// PathUnreadable: it exists, and this user may not read it.
	PathUnreadable
	// PathReadable: it exists and can be read.
	PathReadable
)

// PathCheck stats a local path for ClassifyAt. This package does no I/O:
// the caller passes one (adapters/fsreason.GitPathState), or nil.
type PathCheck func(path string) PathState

// localMissingPath matches git's "'<path>' does not appear to be a git
// repository" for a local path (absolute, or a Windows drive path).
var localMissingPath = regexp.MustCompile(`'((?:/|[A-Za-z]:[\\/])[^']*)' does not appear to be a git repository`)

// localPermission matches a local file system refusal git reports with
// the path quoted ("'<path>': Permission denied", "cannot open '<path>':
// Permission denied"), as opposed to ssh's "Permission denied
// (publickey)" which names no local path.
var localPermission = regexp.MustCompile(`(?i)'((?:/|[A-Za-z]:[\\/])[^']*)'[^\n]*permission denied`)

// LocalPath is the local path git's stderr names as the remote ("" when
// it names none): the path a permissions fix applies to.
func LocalPath(stderr string) string {
	if m := localPermission.FindStringSubmatch(stderr); m != nil {
		return m[1]
	}
	if m := localMissingPath.FindStringSubmatch(stderr); m != nil {
		return m[1]
	}
	return ""
}

// ClassifyAt is Classify with the local path checked: git says "does not
// appear to be a git repository" both for a path that does not exist and
// for one it may not read, so a path check (nil: none) tells Unreadable
// from Missing. A local "'<path>': Permission denied" is Unreadable
// without a check.
func ClassifyAt(stderr string, check PathCheck) Kind {
	k := Classify(stderr)
	if k != Missing || check == nil {
		return k
	}
	if p := LocalPath(stderr); p != "" && check(p) == PathUnreadable {
		return Unreadable
	}
	return k
}

// Classify reads git's stderr of a failed remote call. A stderr naming
// nothing known is Unreachable: lore keeps working and retries, which is
// the safe answer when nothing better is known.
func Classify(stderr string) Kind {
	low := strings.ToLower(stderr)
	switch {
	case localPermission.MatchString(stderr):
		return Unreadable // a local path git may not read, not a credential
	case containsAny(low, deniedText):
		return Denied
	case containsAny(low, unreachableText):
		return Unreachable
	case containsAny(low, missingText) || missingHTTPS.MatchString(low):
		return Missing
	}
	return Unreachable
}

// Reason is the one line of stderr that says why the call failed: the
// first line naming a known failure, else the first line that is not
// git's advice, without git's "fatal: " label, credentials masked, whole
// words only. "" when stderr holds nothing but advice.
func Reason(stderr string) string {
	var first string
	for l := range strings.SplitSeq(stderr, "\n") {
		l = cutPrefix(strings.TrimSpace(l))
		low := strings.ToLower(l)
		if l == "" || containsAny(low, adviceLines) {
			continue
		}
		if first == "" {
			first = l
		}
		if containsAny(low, unreachableText) || containsAny(low, deniedText) || containsAny(low, missingText) || missingHTTPS.MatchString(low) {
			return clip(repourl.MaskText(l))
		}
	}
	return clip(repourl.MaskText(first))
}

// Summary is the start of a sentence naming what failed at shown (a url
// already masked for display): "cannot reach <url>", "no git repository at
// <url>", "<url> refused this machine's credentials".
func Summary(k Kind, shown string) string {
	switch k {
	case Missing:
		return "no git repository at " + shown
	case Denied:
		return shown + " refused this machine's credentials"
	case Unreadable:
		return "cannot read " + shown + " (permission denied)"
	}
	return "cannot reach " + shown
}

// Describe is Summary plus git's Reason in parentheses (when git gave
// one): the whole sentence a doctor line or an error message shows. check
// (nil: none) is ClassifyAt's path check. An unreadable path gets no git
// reason: git's "does not appear to be a git repository" would contradict
// it.
func Describe(stderr, shown string, check PathCheck) string {
	k := ClassifyAt(stderr, check)
	s := Summary(k, shown)
	if k == Unreadable {
		return s
	}
	if r := Reason(stderr); r != "" {
		s += " (" + r + ")"
	}
	return s
}

// cutPrefix removes git's labels ("fatal: ", "remote: ", …) from l.
func cutPrefix(l string) string {
	for again := true; again; {
		again = false
		for _, p := range gitPrefixes {
			if rest, ok := strings.CutPrefix(l, p); ok {
				l, again = strings.TrimSpace(rest), true
			}
		}
	}
	return l
}

// clip bounds s at MaxReason on a word boundary.
func clip(s string) string {
	if len(s) <= MaxReason {
		return s
	}
	cut := strings.ToValidUTF8(s[:MaxReason], "")
	if i := strings.LastIndexByte(cut, ' '); i > MaxReason/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:") + "…"
}

// containsAny reports whether s contains any of words.
func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}
