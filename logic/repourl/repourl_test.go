package repourl

import (
	"slices"
	"strings"
	"testing"
)

// TestNormalizeForms pins that every form of one address is one key, with
// the path case kept (PROV3 needs it kept to notice a case-only change).
func TestNormalizeForms(t *testing.T) {
	want := "github.com/agency/acmehub_backend"
	for _, in := range []string{
		"https://github.com/agency/acmehub_backend.git",
		"https://GitHub.com/agency/acmehub_backend/",
		"git@github.com:agency/acmehub_backend.git",
		"ssh://git@github.com:22/agency/acmehub_backend.git",
		"github.com/agency/acmehub_backend",
	} {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, _ := Normalize("github.com/Agency/acmehub_backend"); got != "github.com/Agency/acmehub_backend" {
		t.Fatalf("path case must be kept, got %q", got)
	}
	for _, bad := range []string{"", "/tmp/repo", "./repo", "file:///tmp/repo", "host-only"} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("Normalize(%q) must fail", bad)
		}
	}
}

// TestStripCredentials pins SQ13: http(s) loses the whole user part, ssh
// keeps the login user and loses a password, scp-like is unchanged.
func TestStripCredentials(t *testing.T) {
	tok := "ghp_" + strings.Repeat("A", 20)
	cases := []struct {
		in, want string
		stripped bool
	}{
		{"https://" + tok + "@github.com/a/b.git", "https://github.com/a/b.git", true},
		{"https://mira:" + tok + "@github.com/a/b.git", "https://github.com/a/b.git", true},
		{"ssh://git:" + tok + "@host/a/b.git", "ssh://git@host/a/b.git", true},
		{"ssh://git@host/a/b.git", "ssh://git@host/a/b.git", false},
		{"git@github.com:a/b.git", "git@github.com:a/b.git", false},
		{"https://github.com/a/b.git", "https://github.com/a/b.git", false},
	}
	for _, c := range cases {
		got, stripped, err := StripCredentials(c.in)
		if err != nil || got != c.want || stripped != c.stripped {
			t.Errorf("StripCredentials(...) = %q, %v, %v; want %q, %v", got, stripped, err, c.want, c.stripped)
		}
		if strings.Contains(got, tok) {
			t.Fatalf("the token survived")
		}
	}
	if _, err := Normalize("https://" + tok + "@x"); err == nil || strings.Contains(err.Error(), tok) {
		t.Fatalf("an error must never print the token: %v", err)
	}
}

// TestSeenRecordsUnconfirmed pins PROV2: a known address in any form
// changes nothing; a new one is appended unconfirmed and is not confirmed.
func TestSeenRecordsUnconfirmed(t *testing.T) {
	h := History{{URL: "git@github.com:agency/x.git", Kind: KindPrimary, How: HowInit, From: "2026-01-01"}}
	same, added, err := h.Seen("https://github.com/agency/x", "2026-02-02", "act")
	if err != nil || added || len(same) != 1 {
		t.Fatalf("a known address in another form must change nothing: %v %v %v", same, added, err)
	}
	next, added, err := h.Seen("https://github.com/tomas/x.git", "2026-02-02", "act")
	if err != nil || !added || len(next) != 2 || next[1].Kind != KindUnconfirmed || next[1].How != HowSeen {
		t.Fatalf("a new address must be appended unconfirmed: %+v", next)
	}
	if slices.Contains(next.Confirmed(), "github.com/tomas/x") {
		t.Fatalf("an unconfirmed address must not be confirmed")
	}
	again, added, _ := next.Seen("https://github.com/tomas/x.git", "2026-03-03", "act")
	if added || len(again) != 2 {
		t.Fatalf("seen twice must not append twice")
	}
}

// TestCaseVariant pins PROV3: a case-only difference is found as the same
// repo.
func TestCaseVariant(t *testing.T) {
	h := History{{URL: "https://github.com/agency/x", Kind: KindPrimary, How: HowInit, From: "2026-01-01"}}
	if k, ok := h.CaseVariant("github.com/Agency/x"); !ok || k != "github.com/agency/x" {
		t.Fatalf("case variant not found: %q %v", k, ok)
	}
	if _, ok := h.CaseVariant("github.com/tomas/x"); ok {
		t.Fatalf("another repo is not a case variant")
	}
}

// TestSetModes pins set-url: primary closes the old one (kept as history
// and still confirmed), mirror adds, remove drops an unconfirmed entry and
// closes a mirror, and the primary cannot be removed.
func TestSetModes(t *testing.T) {
	h := History{
		{URL: "https://github.com/agency/old", Kind: KindPrimary, How: HowInit, From: "2026-01-01"},
		{URL: "https://github.com/tomas/x", Kind: KindUnconfirmed, How: HowSeen, From: "2026-01-05"},
	}
	p, changed, err := h.Set("git@github.com:agency/new.git", SetPrimary, "2026-02-01", "2026-02-01", "act")
	if err != nil || !changed {
		t.Fatal(err)
	}
	if got := p.Confirmed(); !slices.Equal(got, []string{"github.com/agency/new", "github.com/agency/old"}) {
		t.Fatalf("confirmed = %v", got)
	}
	if p[0].To != "2026-02-01" {
		t.Fatalf("old primary must be closed, not deleted: %+v", p[0])
	}
	if _, changed, _ := p.Set("github.com/agency/new", SetPrimary, "x", "x", "act"); changed {
		t.Fatalf("setting the same primary again must be a no-op")
	}
	m, _, merr := p.Set("https://github.com/tomas/x", SetMirror, "2026-02-02", "2026-02-02", "act")
	if merr != nil || m[1].Kind != KindMirror {
		t.Fatalf("an unconfirmed entry must be promoted to mirror: %+v %v", m, merr)
	}
	r, _, err := m.Set("github.com/tomas/x", SetRemove, "", "2026-03-01", "act")
	if err != nil || r[1].To != "2026-03-01" {
		t.Fatalf("remove must close a mirror: %+v %v", r, err)
	}
	if _, _, rerr := r.Set("github.com/agency/new", SetRemove, "", "x", "act"); rerr == nil {
		t.Fatalf("the primary cannot be removed")
	}
	u, _, err := h.Set("github.com/tomas/x", SetRemove, "", "x", "act")
	if err != nil || len(u) != 1 {
		t.Fatalf("remove must drop an unconfirmed entry: %+v %v", u, err)
	}
}

// TestStripRemoved pins 14.3/14.4: what was removed decides the warning;
// a plain user name is dropped silently, a token user or password warns;
// an unreadable value with "@" is refused; Strip is idempotent.
func TestStripRemoved(t *testing.T) {
	tok := "ghp_" + strings.Repeat("A", 20)
	random := "Zq8xK2pL9mW4vB7nR3tY6uE1" // 24 chars, high entropy
	cases := []struct {
		in, want string
		removed  Removed
	}{
		{"https://mira@github.com/a/b.git", "https://github.com/a/b.git", RemovedUser},
		{"https://" + tok + "@github.com/a/b.git", "https://github.com/a/b.git", RemovedTokenUser},
		{"https://" + random + "@github.com/a/b.git", "https://github.com/a/b.git", RemovedTokenUser},
		{"https://x-access-token:" + tok + "@github.com/a/b", "https://github.com/a/b", RemovedPassword},
		{"ssh://git:" + tok + "@host:2222/a/b.git", "ssh://git@host:2222/a/b.git", RemovedPassword},
		{"ssh://git@host/a/b.git", "ssh://git@host/a/b.git", RemovedNone},
		{"git@github.com:a/b.git", "git@github.com:a/b.git", RemovedNone},
		{"https://GitHub.com/A/b.git?x=1#f", "https://GitHub.com/A/b.git?x=1#f", RemovedNone},
		{"github.com/a/b", "github.com/a/b", RemovedNone},
	}
	for _, c := range cases {
		got, removed, err := Strip(c.in)
		if err != nil || got != c.want || removed != c.removed {
			t.Errorf("Strip(case %q) = %q, %v, %v; want %q, %v", c.want, got, removed, err, c.want, c.removed)
		}
		again, r2, err := Strip(got)
		if err != nil || again != got || (r2 != RemovedNone && r2 != c.removed) {
			t.Errorf("Strip is not idempotent on %q: %q %v %v", got, again, r2, err)
		}
		if c.removed.Warn() != (c.removed == RemovedPassword || c.removed == RemovedTokenUser) {
			t.Errorf("Warn mismatch for %v", c.removed)
		}
	}
	for _, bad := range []string{"mira:" + tok + "@github.com/a/b", "git:" + tok + "@host:a/b"} {
		if _, _, err := Strip(bad); err == nil {
			t.Errorf("a scheme-less value with a credential must be refused")
		} else if strings.Contains(err.Error(), tok) {
			t.Fatalf("the error printed the token")
		}
	}
}
