package orgconf

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// fakeSSH is an ssh config with one alias: work-gh → github.com (User git),
// and bare-alias → git.example.test with no User.
func fakeSSH(host string) (SSHAlias, bool) {
	switch host {
	case "work-gh":
		return SSHAlias{HostName: "github.com", User: "git"}, true
	case "bare-alias":
		return SSHAlias{HostName: "git.example.test"}, true
	}
	return SSHAlias{}, false
}

// TestCanonicalSSH: an ssh-alias url resolves to its canonical url and a
// rule mapping the canonical prefix back to the typed one; any other url
// is ErrNotAlias.
func TestCanonicalSSH(t *testing.T) {
	for _, c := range []struct {
		raw, url, ruleURL, via string
	}{
		{"git@work-gh:acme/lore-x.git", "git@github.com:acme/lore-x.git", "git@github.com:acme/", "git@work-gh:acme/"},
		{"work-gh:acme/lore-x.git", "git@github.com:acme/lore-x.git", "git@github.com:acme/", "work-gh:acme/"},
		{"bare-alias:acme/n", "git@git.example.test:acme/n", "git@git.example.test:acme/", "bare-alias:acme/"},
		{"ssh://git@work-gh/acme/lore-x.git", "ssh://git@github.com/acme/lore-x.git", "ssh://git@github.com/acme/", "ssh://git@work-gh/acme/"},
		{"ssh://work-gh:2222/acme/x?branch=dev", "ssh://git@github.com:2222/acme/x?branch=dev", "ssh://git@github.com:2222/acme/", "ssh://work-gh:2222/acme/"},
		// a repo with no owner segment maps only itself
		{"git@work-gh:solo.git", "git@github.com:solo.git", "git@github.com:solo.git", "git@work-gh:solo.git"},
	} {
		got, err := CanonicalSSH(c.raw, fakeSSH)
		if err != nil {
			t.Errorf("CanonicalSSH(%q): %v", c.raw, err)
			continue
		}
		if got.URL != c.url || got.Rule.URL != c.ruleURL || got.Rule.Via != c.via {
			t.Errorf("CanonicalSSH(%q) = %+v, want url %s rule %s via %s", c.raw, got, c.url, c.ruleURL, c.via)
		}
		// the rule maps the canonical url back to the typed spelling, and
		// never the whole host (D-ah)
		if !strings.HasPrefix(got.URL, got.Rule.URL) {
			t.Errorf("%q: canonical %s does not start with the rule's url %s", c.raw, got.URL, got.Rule.URL)
		}
		if HostWide(got.Rule) {
			t.Errorf("%q: recorded rule %+v maps the whole host", c.raw, got.Rule)
		}
		// another owner's repo on the same host is untouched by the rule
		if other := strings.Replace(got.URL, "acme/", "someone/", 1); other != got.URL && AccessArgs([]Access{got.Rule}, other) != nil {
			t.Errorf("%q: the rule %+v also maps %s", c.raw, got.Rule, other)
		}
	}
	for _, raw := range []string{"git@github.com:acme/x.git", "https://work-gh/acme/x.git", "/srv/x.git", "file:///srv/x", "C:/x", "git@unknown:acme/x"} {
		if _, err := CanonicalSSH(raw, fakeSSH); !errors.Is(err, ErrNotAlias) {
			t.Errorf("CanonicalSSH(%q) err %v, want ErrNotAlias", raw, err)
		}
	}
}

// TestAccessArgs: a remote starting with a rule's url gets the -c pair;
// others get none.
func TestAccessArgs(t *testing.T) {
	rules := []Access{{URL: "git@github.com:", Via: "git@work-gh:"}, {URL: "ssh://git@git.example.test/", Via: "file:///srv/mirror/"}}
	got := AccessArgs(rules, "git@github.com:acme/x.git")
	if !slices.Equal(got, []string{"-c", "url.git@work-gh:.insteadOf=git@github.com:"}) {
		t.Errorf("args = %q", got)
	}
	if got := AccessArgs(rules, "https://github.com/acme/x.git"); got != nil {
		t.Errorf("https url got %q", got)
	}
	if r, ok := AccessFor(rules, "ssh://git@git.example.test/a/b"); !ok || r.Via != "file:///srv/mirror/" {
		t.Errorf("AccessFor = %+v %v", r, ok)
	}
}

// TestAccessValidate: both prefixes required, different, one rule per url.
func TestAccessValidate(t *testing.T) {
	ok := "access:\n  - url: \"git@github.com:\"\n    via: \"git@work-gh:\"\n"
	if _, err := Parse([]byte(ok)); err != nil {
		t.Fatalf("a valid access rule was refused: %v", err)
	}
	for _, bad := range []string{
		"access:\n  - url: \"git@github.com:\"\n",
		"access:\n  - url: \"git@github.com:\"\n    via: \"git@github.com:\"\n",
		ok + "  - url: \"git@github.com:\"\n    via: \"git@other:\"\n",
	} {
		if _, err := Parse([]byte(bad)); err == nil || !strings.Contains(err.Error(), "access[") {
			t.Errorf("Parse(%q) = %v, want an access[i] refusal", bad, err)
		}
	}
}

// TestFixFor: the exact config.yaml lines and the git config command.
func TestFixFor(t *testing.T) {
	f := FixFor(Access{URL: "git@github.com:", Via: "git@myalias:"})
	if want := "access:\n  - url: \"git@github.com:\"\n    via: \"git@myalias:\""; f.YAML != want {
		t.Errorf("YAML = %q, want %q", f.YAML, want)
	}
	if want := `git config --global url."git@myalias:".insteadOf "git@github.com:"`; f.GitCommand != want {
		t.Errorf("GitCommand = %q, want %q", f.GitCommand, want)
	}
	if _, err := Parse([]byte(f.YAML + "\n")); err != nil {
		t.Errorf("the fix's YAML does not parse as config.yaml: %v", err)
	}
}

// TestRuleVia: the rule that reaches a canonical url through an alias.
func TestRuleVia(t *testing.T) {
	for _, c := range []struct{ canon, alias, url, via string }{
		{"git@github.com:acme/x.git", "work-gh", "git@github.com:acme/", "git@work-gh:acme/"},
		{"ssh://git@github.com/acme/x.git", "work-gh", "ssh://git@github.com/acme/", "ssh://git@work-gh/acme/"},
		{"https://github.com/acme/x.git", "work-gh", "https://github.com/acme/", "git@work-gh:acme/"},
	} {
		r, ok := RuleVia(c.canon, c.alias)
		if !ok || r.URL != c.url || r.Via != c.via {
			t.Errorf("RuleVia(%q, %q) = %+v %v, want %s → %s", c.canon, c.alias, r, ok, c.url, c.via)
		}
		if HostWide(r) {
			t.Errorf("RuleVia(%q) suggests a host-wide rule %+v", c.canon, r)
		}
		if f := FixFor(r); !strings.Contains(f.GitCommand, "acme/") {
			t.Errorf("the git command %q is not scoped to the owner", f.GitCommand)
		}
	}
	if _, ok := RuleVia("/srv/x.git", "work-gh"); ok {
		t.Error("a local path got a rule")
	}
}

// TestHostWide: which rules map a whole host, the owner of a url, and the
// owner-scoped form of a host-wide rule.
func TestHostWide(t *testing.T) {
	for u, want := range map[string]bool{
		"git@github.com:": true, "ssh://git@github.com/": true, "https://github.com/": true, "https://github.com": true,
		"git@github.com:acme/": false, "https://github.com/acme/": false, "ssh://git@github.com/acme/": false, "file:///srv/mirror/": false, "/srv/m/": false,
	} {
		if got := HostWide(Access{URL: u, Via: "x"}); got != want {
			t.Errorf("HostWide(%q) = %v, want %v", u, got, want)
		}
	}
	for u, want := range map[string]string{"git@github.com:acme/x.git": "acme", "https://github.com/acme/x": "acme", "ssh://git@h:22/acme/x": "acme", "git@github.com:x.git": "", "/srv/a/b": ""} {
		if got := OwnerOf(u); got != want {
			t.Errorf("OwnerOf(%q) = %q, want %q", u, got, want)
		}
	}
	wide := Access{URL: "git@github.com:", Via: "git@work-gh:"}
	if got := OwnerScoped(wide, "git@github.com:acme/x.git"); got != (Access{URL: "git@github.com:acme/", Via: "git@work-gh:acme/"}) {
		t.Errorf("OwnerScoped = %+v", got)
	}
	if h, ok := RemoteHost("https://github.com/acme/x"); !ok || h != "github.com" {
		t.Errorf("RemoteHost https = %q %v", h, ok)
	}
	if h, ok := RemoteHost("ssh://git@Example.test:2222/a/b"); !ok || h != "example.test" {
		t.Errorf("RemoteHost ssh = %q %v", h, ok)
	}
}

// TestAccessFailureNote: the note names the mapping and where it is set;
// for a host-wide rule it says how to narrow it.
func TestAccessFailureNote(t *testing.T) {
	n := AccessFailureNote(Access{URL: "git@github.com:acme/", Via: "git@work-gh:acme/"})
	for _, want := range []string{"git@github.com:acme/ → git@work-gh:acme/", "~/.lore/config.yaml", "ssh -T git@work-gh"} {
		if !strings.Contains(n, want) {
			t.Errorf("note %q lacks %q", n, want)
		}
	}
	if strings.Contains(n, "every repo") {
		t.Errorf("an owner rule's note talks about the whole host: %q", n)
	}
	if w := AccessFailureNote(Access{URL: "git@github.com:", Via: "git@work-gh:"}); !strings.Contains(w, "every repo of that host") || !strings.Contains(w, "git@github.com:<owner>/") {
		t.Errorf("host-wide note %q", w)
	}
}
