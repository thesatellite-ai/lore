package gitfault

import (
	"strings"
	"testing"
)

// gitAdvice is what git appends to every failed remote call.
const gitAdvice = "fatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n"

func TestClassifyAndReason(t *testing.T) {
	cases := []struct {
		name, stderr string
		kind         Kind
		reason       string
	}{
		{"missing path", "fatal: '/srv/none/x.git' does not appear to be a git repository\n" + gitAdvice, Missing, "'/srv/none/x.git' does not appear to be a git repository"},
		{"dns over ssh", "ssh: Could not resolve hostname nohost.example: Name or service not known\n" + gitAdvice, Unreachable, "ssh: Could not resolve hostname nohost.example: Name or service not known"},
		{"publickey", "git@host.example: Permission denied (publickey).\n" + gitAdvice, Denied, "git@host.example: Permission denied (publickey)."},
		{"hosted not found", "ERROR: Repository not found.\n" + gitAdvice, Missing, "Repository not found."},
		{"https not found", "remote: Repository not found.\nfatal: repository 'https://host.example/o/x.git/' not found\n", Missing, "Repository not found."},
		{"https dns", "fatal: unable to access 'https://host.example/x.git/': Could not resolve host: host.example\n", Unreachable, "unable to access 'https://host.example/x.git/': Could not resolve host: host.example"},
		{"only advice", gitAdvice, Unreachable, ""},
		{"unknown", "fatal: something odd\n", Unreachable, "something odd"},
	}
	for _, c := range cases {
		if got := Classify(c.stderr); got != c.kind {
			t.Errorf("%s: Classify = %q, want %q", c.name, got, c.kind)
		}
		if got := Reason(c.stderr); got != c.reason {
			t.Errorf("%s: Reason = %q, want %q", c.name, got, c.reason)
		}
	}
}

// The reason is never the tail of git's advice (B3-12 printed "and the
// repository exists.").
func TestReasonNeverAdviceTail(t *testing.T) {
	for _, s := range []string{gitAdvice, "fatal: '/x' does not appear to be a git repository\n" + gitAdvice} {
		if r := Reason(s); strings.Contains(r, "repository exists") || strings.Contains(r, "access rights") {
			t.Fatalf("Reason(%q) = %q", s, r)
		}
	}
}

func TestReasonMasksCredentials(t *testing.T) {
	secret := "pw" + "123456"
	s := "fatal: unable to access 'https://mira:" + secret + "@host.example/x.git/': Could not resolve host: host.example\n"
	if r := Reason(s); strings.Contains(r, secret) || !strings.Contains(r, "mira:<redacted>@host.example") {
		t.Fatalf("Reason = %q", r)
	}
}

func TestReasonWholeWords(t *testing.T) {
	r := Reason("fatal: " + strings.Repeat("word ", 100))
	if len(r) > MaxReason+len("…") || !strings.HasSuffix(r, "word…") {
		t.Fatalf("Reason = %q", r)
	}
}

func TestDescribe(t *testing.T) {
	got := Describe("fatal: '/srv/x.git' does not appear to be a git repository\n"+gitAdvice, "ssh://host.example/x.git", nil)
	want := "no git repository at ssh://host.example/x.git ('/srv/x.git' does not appear to be a git repository)"
	if got != want {
		t.Fatalf("Describe = %q, want %q", got, want)
	}
	if got := Describe(gitAdvice, "u", nil); got != "cannot reach u" {
		t.Fatalf("Describe(advice) = %q", got)
	}
}

// B3-22: git says "does not appear to be a git repository" for a local
// path it may not read too; the path check tells it from a missing one,
// and the sentence says the path cannot be read, not "fix the address".
func TestUnreadableLocalPath(t *testing.T) {
	stderr := "fatal: '/srv/locked/site.git' does not appear to be a git repository\n" + gitAdvice
	states := map[PathState]Kind{PathUnreadable: Unreadable, PathAbsent: Missing, PathUnknown: Missing, PathReadable: Missing}
	for st, want := range states {
		var asked string
		got := ClassifyAt(stderr, func(p string) PathState { asked = p; return st })
		if got != want || asked != "/srv/locked/site.git" {
			t.Errorf("path state %d: ClassifyAt = %q (asked %q), want %q", st, got, asked, want)
		}
	}
	if got := ClassifyAt(stderr, nil); got != Missing {
		t.Errorf("no check: %q", got)
	}
	unreadable := func(string) PathState { return PathUnreadable }
	if got := Describe(stderr, "origin (/srv/locked/site.git)", unreadable); got != "cannot read origin (/srv/locked/site.git) (permission denied)" {
		t.Errorf("Describe = %q", got)
	}
	// git's own local refusal names the path: unreadable without a check,
	// never mistaken for ssh's credential refusal
	local := "fatal: '/srv/locked/site.git/HEAD': Permission denied\n" + gitAdvice
	if got := Classify(local); got != Unreadable || LocalPath(local) != "/srv/locked/site.git/HEAD" {
		t.Errorf("local permission: %q %q", got, LocalPath(local))
	}
	if got := Classify("git@host.example: Permission denied (publickey).\n" + gitAdvice); got != Denied {
		t.Errorf("ssh publickey: %q", got)
	}
}
