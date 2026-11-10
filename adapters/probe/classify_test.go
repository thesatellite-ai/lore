package probe

import (
	"strings"
	"testing"
)

// The remote classifier over real host messages (ONB 4.3, 4.4).
func TestClassifyRemote(t *testing.T) {
	cases := []struct {
		name, stderr string
		want         Class
	}{
		{"github ssh read-only", "ERROR: Permission to acme-hub/lore-team.git denied to mira.\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.", ClassNoWrite},
		{"github https read-only", "remote: Permission to acme-hub/lore-team.git denied to mira.\nfatal: unable to access 'https://github.com/acme-hub/lore-team.git/': The requested URL returned error: 403", ClassNoWrite},
		{"gitlab no push", "remote: You are not allowed to push code to this project.\nfatal: Could not read from remote repository.", ClassNoWrite},
		{"github not found", "ERROR: Repository not found.\nfatal: Could not read from remote repository.", ClassNoAccess},
		{"local not a repo", "fatal: '/x/nothere.git' does not appear to be a git repository\nfatal: Could not read from remote repository.", ClassNoAccess},
		{"https 404", "fatal: repository 'https://github.example/a/b.git/' not found", ClassNoAccess},
		{"publickey", "git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.", ClassAuthDenied},
		{"https auth", "fatal: Authentication failed for 'https://github.com/a/b.git/'", ClassAuthDenied},
		{"no prompt", "fatal: could not read Username for 'https://github.com': terminal prompts disabled", ClassAuthDenied},
		{"dns", "ssh: Could not resolve hostname github-acme: Name or service not known\nfatal: Could not read from remote repository.", ClassUnreachable},
		{"refused", "ssh: connect to host github.com port 22: Connection refused", ClassUnreachable},
		{"https dns", "fatal: unable to access 'https://github.com/a/b.git/': Could not resolve host: github.com", ClassUnreachable},
		{"host key", "Host key verification failed.\nfatal: Could not read from remote repository.", ClassHostKey},
		{"unknown", "fatal: something odd happened", ClassFailed},
		{"empty", "", ClassFailed},
	}
	for _, c := range cases {
		if got := classifyRemote(c.stderr); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestClassifyGH(t *testing.T) {
	cases := map[string]Class{
		"gh: Not Found (HTTP 404)":                                   ClassNoAccess,
		"gh: Bad credentials (HTTP 401)":                             ClassNotLoggedIn,
		"To get started with GitHub CLI, please run:  gh auth login": ClassNotLoggedIn,
		"gh: Resource not accessible by integration (HTTP 403)":      ClassAuthDenied,
		"error connecting to api.github.com":                         ClassUnreachable,
		"something else":                                             ClassFailed,
	}
	for in, want := range cases {
		if got := classifyGH(in); got != want {
			t.Errorf("%q: got %s, want %s", in, got, want)
		}
	}
}

// Sanitize never lets a credential through (ONB 4.4 store.url).
func TestSanitize(t *testing.T) {
	tok := "ghp_" + strings.Repeat("A1b2", 9)
	cases := []struct{ in, mustNot, want string }{
		{"fatal: unable to access 'https://mira:" + tok + "@github.com/a/b.git/'", tok, "fatal: unable to access 'https://github.com/a/b.git/'"},
		{"https://" + tok + "@github.com/a/b", tok, "https://github.com/a/b"},
		{"token " + tok + " leaked", tok, "token <redacted> leaked"},
		{"glpat-" + strings.Repeat("x", 20), "glpat-", "<redacted>"},
		{"line one\nline two", "\n", "line one line two"},
		{"git@github.com:acme-hub/r.git", "", "git@github.com:acme-hub/r.git"},
	}
	for _, c := range cases {
		got := Sanitize(c.in)
		if c.mustNot != "" && strings.Contains(got, c.mustNot) {
			t.Errorf("Sanitize(%q) kept %q: %q", c.in, c.mustNot, got)
		}
		if got != c.want {
			t.Errorf("Sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if long := Sanitize(strings.Repeat("a", 500)); len(long) > maxDetail+4 {
		t.Errorf("not truncated: %d", len(long))
	}
}

// Q5: the alias rewrite, the key fallback, and refusal of odd input.
func TestRoute(t *testing.T) {
	a := Route{HostAlias: "github-acme"}
	got := strings.Join(a.GitArgs(), " ")
	if want := "-c url.git@github-acme:.insteadOf=git@github.com: -c url.ssh://git@github-acme/.insteadOf=ssh://git@github.com/"; got != want {
		t.Errorf("GitArgs = %q", got)
	}
	if strings.Contains(a.GitSSHCommand("/h"), "-i") {
		t.Errorf("alias route must not add -i: %q", a.GitSSHCommand("/h"))
	}
	k := Route{SSHKey: "~/.ssh/id acme"}
	if k.GitArgs() != nil {
		t.Errorf("key route has no git args")
	}
	if got, want := k.GitSSHCommand("/home/mira"), "ssh -o BatchMode=yes -o ConnectTimeout=5 -i '/home/mira/.ssh/id acme' -o IdentitiesOnly=yes"; got != want {
		t.Errorf("GitSSHCommand = %q, want %q", got, want)
	}
	if got := (Route{SSHKey: "/k/it's"}).GitSSHCommand(""); !strings.Contains(got, `'/k/it'\''s'`) {
		t.Errorf("quote: %q", got)
	}
	for _, bad := range []Route{{HostAlias: "-oProxyCommand=x"}, {HostAlias: "a b"}, {HostAlias: "a;b"}, {SSHKey: "-oProxyCommand=x"}, {SSHKey: "a\nb"}} {
		if bad.Validate() == nil {
			t.Errorf("Validate(%+v) passed", bad)
		}
	}
}

func TestCleanURL(t *testing.T) {
	tok := "ghp_" + strings.Repeat("Z9y8", 9)
	for _, bad := range []string{"https://mira:" + tok + "@github.com/a/b.git", "https://" + tok + "@github.com/a/b.git", "-uhack", ""} {
		if _, o := cleanURL(bad); o == nil || o.Class != ClassBadInput || strings.Contains(o.Detail, tok) {
			t.Errorf("cleanURL(%q) = %+v", bad, o)
		}
	}
	if u, o := cleanURL("https://mira@github.com/a/b.git"); o != nil || u != "https://github.com/a/b.git" {
		t.Errorf("plain user: %q %+v", u, o)
	}
	if u, o := cleanURL("git@github.com:acme-hub/r.git"); o != nil || u != "git@github.com:acme-hub/r.git" {
		t.Errorf("scp: %q %+v", u, o)
	}
}
