package wire

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// TestAccountEmails reads each login's emails with its own token
// (discoverWorld): a token with user:email lists them; one without, a
// revoked token and a login gh has no token for are unreadable with a
// fixed reason that never carries a token.
func TestAccountEmails(t *testing.T) {
	t.Parallel()
	p, api, toks := discoverWorld(t)
	cases := []struct {
		user     string
		readable bool
		emails   []string
		why      string
	}{
		{"mira", true, []string{"mira@personal.example", "mira@other.example"}, ""},
		{"acme-bot", false, nil, whyNoEmailRead},
		{"revoked", false, nil, whyAPIFailed},
		{"nogh", false, nil, whyNoGHLogin},
	}
	for _, c := range cases {
		t.Run(c.user, func(t *testing.T) {
			ok, emails, why := accountEmails(context.Background(), p, api, c.user)
			if ok != c.readable || !slices.Equal(emails, c.emails) || why != c.why {
				t.Fatalf("got (%v, %v, %q), want (%v, %v, %q)", ok, emails, why, c.readable, c.emails, c.why)
			}
			for _, tok := range toks {
				if strings.Contains(why, tok) {
					t.Fatalf("reason leaks a token: %q", why)
				}
			}
		})
	}
}
