package onboard

import (
	"strings"
	"testing"
)

// TestIdentityEmailMatch walks emails × account × readability: a match in
// any identity email (any case, spaces) passes, a readable list without it
// warns naming the email and the list, an unreadable list never warns.
func TestIdentityEmailMatch(t *testing.T) {
	acct := []string{"person@example.com", "pat@personal.example", "1001+octo-personal@users.noreply.github.com"}
	cases := []struct {
		name     string
		in       IdentityEmailIn
		want     Status
		contains []string
	}{
		{"no identity email", IdentityEmailIn{User: "octo-personal", Readable: true, AccountEmails: acct}, StatusSkip, []string{"no identity email"}},
		{"blank identity email", IdentityEmailIn{Emails: []string{"  "}, User: "octo-personal", Readable: true, AccountEmails: acct}, StatusSkip, []string{"no identity email"}},
		{"no account", IdentityEmailIn{Emails: []string{"person@example.com"}, Readable: true, AccountEmails: acct}, StatusSkip, []string{"no GitHub account"}},
		{"primary matches", IdentityEmailIn{Emails: []string{"person@example.com"}, User: "octo-personal", Readable: true, AccountEmails: acct}, StatusOK, []string{"person@example.com is one of octo-personal's"}},
		{"case and spaces", IdentityEmailIn{Emails: []string{" Person@Example.COM "}, User: "octo-personal", Readable: true, AccountEmails: acct}, StatusOK, nil},
		{"second identity email matches", IdentityEmailIn{Emails: []string{"work@corp.example", "pat@personal.example"}, User: "octo-personal", Readable: true, AccountEmails: acct}, StatusOK, []string{"pat@personal.example"}},
		{"noreply matches", IdentityEmailIn{Emails: []string{"1001+octo-personal@users.noreply.github.com"}, User: "octo-personal", Readable: true, AccountEmails: acct}, StatusOK, nil},
		{"other account warns", IdentityEmailIn{Emails: []string{"person@example.com"}, User: "octo-work", Readable: true, AccountEmails: []string{"work@example.org"}}, StatusFail,
			[]string{"person@example.com is not one of octo-work's GitHub emails", "work@example.org"}},
		{"readable but empty list warns", IdentityEmailIn{Emails: []string{"person@example.com"}, User: "x", Readable: true}, StatusFail, []string{"(none)"}},
		{"unreadable never warns", IdentityEmailIn{Emails: []string{"person@example.com"}, User: "octo-work", Why: "no scope"}, StatusSkip, []string{"could not read octo-work's GitHub emails (no scope)"}},
		{"unreadable default reason", IdentityEmailIn{Emails: []string{"person@example.com"}, User: "octo-work"}, StatusSkip, []string{identityEmailWhyDefault}},
		{"unreadable even with stale list", IdentityEmailIn{Emails: []string{"person@example.com"}, User: "u", AccountEmails: []string{"other@x.example"}}, StatusSkip, []string{"could not read"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, detail := IdentityEmailMatch(c.in)
			if st != c.want {
				t.Fatalf("status %s, want %s (detail %q)", st, c.want, detail)
			}
			for _, s := range c.contains {
				if !strings.Contains(detail, s) {
					t.Errorf("detail %q lacks %q", detail, s)
				}
			}
		})
	}
}

// TestFoldersReachable walks folders × default × org × disabled: only a
// setup with none of folder, default or org warns; disabled always skips.
func TestFoldersReachable(t *testing.T) {
	for _, folders := range [][]string{nil, {}, {" "}, {"~/work/**"}} {
		for _, def := range []bool{false, true} {
			for _, org := range []string{"", "acme"} {
				for _, disabled := range []bool{false, true} {
					in := FoldersReachIn{Folders: folders, Default: def, Org: org, Disabled: disabled}
					st, detail := FoldersReachable(in)
					hasFolder := len(folders) == 1 && folders[0] != " "
					var want Status
					switch {
					case disabled:
						want = StatusSkip
					case hasFolder || def || org != "":
						want = StatusOK
					default:
						want = StatusFail
					}
					if st != want {
						t.Errorf("%+v: status %s, want %s (%q)", in, st, want, detail)
					}
					if want == StatusFail && detail != DetailNeverPicked {
						t.Errorf("%+v: detail %q", in, detail)
					}
				}
			}
		}
	}
	if _, d := FoldersReachable(FoldersReachIn{Org: " acme "}); d != "lore picks it in repos of org acme" {
		t.Errorf("org detail %q", d)
	}
}
