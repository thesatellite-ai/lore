package gitleaks

import "testing"

// TestMaskedURLExampleNotASecret: docs show credentials masked as
// `https://user:<redacted>@host`; an older hook flagged that example as
// url_with_password and blocked every commit touching the page (earlier
// output round). The masked forms docs use are no finding; the same url
// with a real-looking password still is.
func TestMaskedURLExampleNotASecret(t *testing.T) {
	t.Parallel()
	s := full(t)
	for _, x := range []string{
		"https://user:<redacted>@host",
		"https://user:<redacted>@git.example.com/org/repo.git",
		"git clone https://bot:<token>@example.com/x.git",
		"https://user:***@host/path",
	} {
		if fs := s.Scan("body", x); len(fs) > 0 {
			t.Errorf("masked example flagged by %v: %q", ruleIDs(fs), x)
		}
	}
	real := "https://user:" + "Hq4" + "vT9wLm2Zr8" + "@host"
	if !ruleIDs(s.Scan("body", real))[RuleURLWithPassword] {
		t.Errorf("a real-looking url password is no longer flagged by %s", RuleURLWithPassword)
	}
}
