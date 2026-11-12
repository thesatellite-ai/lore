package orgconf

// access.go — how this machine reaches a store whose shared config names
// its canonical url (brief 3.7). Shared files (lore-org.yaml, a repo's
// .lore/stores.yaml) name a store as everyone can reach it
// (git@github.com:acme/notes.git, https://…); a person whose machine reaches
// that host through an ssh alias (`Host work-gh` with `HostName github.com`
// in ~/.ssh/config) says so once in their own config.yaml:
//
//	access:
//	  - url: git@github.com:acme/     # the canonical prefix, as shared config spells it
//	    via: git@work-gh:acme/        # how this machine reaches it
//
// Every rule lore suggests or records is scoped to the owner/org path
// (git@github.com:acme/, https://github.com/acme/), never the whole host:
// a host-wide rule (url: git@github.com:) sends every repo of that host
// through the alias's account, so a store of another account (a personal
// store) is refused (D-ah). A host-wide rule a person wrote still works;
// doctor warns about it (HostWide, OwnerScoped give the narrower lines).
//
// Every git network call lore makes to a store whose url starts with url
// gets `-c url.<via>.insteadOf=<url>` (AccessArgs): per call, never
// written into any git config, the person's global config untouched.
//
//	AccessArgs(rules, remote)        ─▶ the -c pairs of the rules remote starts with
//	CanonicalSSH(raw, resolve)       ─▶ an ssh-alias url ─▶ its canonical url
//	                                    plus the access rule mapping it back
//	AliasFix(canonical, via)         ─▶ the exact config.yaml lines and the
//	                                    git config command doctor prints

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// AccessKey is config.yaml's key for this machine's access rules.
const AccessKey = "access"

// Access is one `access:` rule: a remote starting with URL is reached as
// Via (git's url.<Via>.insteadOf=<URL>).
type Access struct {
	// URL is the canonical prefix as shared config spells it
	// (git@github.com:acme/, ssh://git@github.com/acme/,
	// https://github.com/acme/).
	URL string `yaml:"url"`
	// Via is the prefix this machine uses instead (git@work-gh:acme/).
	Via string `yaml:"via"`
}

// validateAccess checks the access rules: both prefixes set, different,
// and each url once.
func validateAccess(rules []Access) []error {
	var all []error
	seen := map[string]int{}
	for i, r := range rules {
		w := fmt.Sprintf("%s[%d]", AccessKey, i)
		u, v := strings.TrimSpace(r.URL), strings.TrimSpace(r.Via)
		switch {
		case u == "" || v == "":
			all = append(all, fmt.Errorf("%s: both url (the canonical prefix shared config uses) and via (how this machine reaches it) are required", w))
			continue
		case u == v:
			all = append(all, fmt.Errorf("%s: url and via are both %q; a rule that maps a prefix to itself does nothing, drop it", w, u))
			continue
		case strings.ContainsAny(u+v, " \t\n"):
			all = append(all, fmt.Errorf("%s: url and via are url prefixes and hold no spaces", w))
			continue
		}
		if j, dup := seen[u]; dup {
			all = append(all, fmt.Errorf("%s: url %q is already mapped by %s[%d]; keep one rule per prefix", w, u, AccessKey, j))
		}
		seen[u] = i
	}
	return all
}

// AccessArgs is the git global options that reach remote through rules:
// `-c url.<via>.insteadOf=<url>` for every rule whose url remote starts
// with (git itself picks the longest match). None when no rule matches.
func AccessArgs(rules []Access, remote string) []string {
	var out []string
	for _, r := range rules {
		u, v := strings.TrimSpace(r.URL), strings.TrimSpace(r.Via)
		if u == "" || v == "" || !strings.HasPrefix(remote, u) {
			continue
		}
		out = append(out, "-c", InsteadOfPair(v, u))
	}
	return out
}

// InsteadOfPair is the `-c` value mapping prefix from to via.
func InsteadOfPair(via, from string) string {
	return "url." + via + ".insteadOf=" + from
}

// AccessFor is the rule of rules whose url remote starts with, longest
// first, as git picks it; ok false when none does.
func AccessFor(rules []Access, remote string) (Access, bool) {
	var best Access
	found := false
	for _, r := range rules {
		u := strings.TrimSpace(r.URL)
		if u != "" && strings.HasPrefix(remote, u) && (!found || len(u) > len(best.URL)) {
			best, found = Access{URL: u, Via: strings.TrimSpace(r.Via)}, true
		}
	}
	return best, found
}

// SSHAlias is what an ssh config says about a host name: the HostName it
// connects to and the User (both "" when unset).
type SSHAlias struct {
	HostName string
	User     string
}

// AliasResolver answers what ssh would connect to for host; ok false when
// the ssh config sets no HostName other than host itself.
type AliasResolver func(host string) (SSHAlias, bool)

// DefaultSSHUser is the user of a canonical scp-like url when neither the
// typed url nor the ssh config names one (the git hosts' ssh user).
const DefaultSSHUser = "git"

// Canonicalized is an ssh-alias url resolved to its canonical form.
type Canonicalized struct {
	// URL is the canonical url shared config stores.
	URL string
	// Alias is the alias host typed, HostName what it resolves to.
	Alias, HostName string
	// Rule maps URL back to the person's spelling on this machine.
	Rule Access
}

// ErrNotAlias is CanonicalSSH's answer for a url that names no ssh alias
// (an https url, a host the ssh config does not rename, a local path).
var ErrNotAlias = errors.New("orgconf: the url names no ssh alias")

// CanonicalSSH resolves an ssh url whose host is an ssh alias (resolve
// answers ok) to the canonical url: the alias replaced by its HostName,
// the user kept as typed, else the alias's User, else DefaultSSHUser. The
// rule maps the canonical prefix (user@host: or ssh://user@host/) back to
// the typed one, so every git call still reaches the store through the
// alias. A url that is no ssh alias url is ErrNotAlias.
func CanonicalSSH(raw string, resolve AliasResolver) (Canonicalized, error) {
	s := strings.TrimSpace(raw)
	if resolve == nil || s == "" {
		return Canonicalized{}, ErrNotAlias
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "ssh" || u.Hostname() == "" {
			return Canonicalized{}, ErrNotAlias
		}
		host := u.Hostname()
		al, ok := resolve(host)
		if !ok || al.HostName == "" || strings.EqualFold(al.HostName, host) {
			return Canonicalized{}, ErrNotAlias
		}
		user := u.User.Username()
		if user == "" {
			user = orDefaultUser(al.User)
		}
		typedPrefix := "ssh://" + u.Host + "/"
		if u.User != nil {
			typedPrefix = "ssh://" + u.User.Username() + "@" + u.Host + "/"
		}
		canonHost := strings.ToLower(al.HostName)
		if p := u.Port(); p != "" {
			canonHost += ":" + p
		}
		canonPrefix := "ssh://" + user + "@" + canonHost + "/"
		canonURL := canonPrefix + strings.TrimPrefix(u.EscapedPath(), "/") + querySuffix(u)
		return Canonicalized{URL: canonURL, Alias: host, HostName: strings.ToLower(al.HostName),
			Rule: OwnerScoped(Access{URL: canonPrefix, Via: typedPrefix}, canonURL)}, nil
	}
	if strings.HasPrefix(s, "/") || !strings.Contains(s, ":") {
		return Canonicalized{}, ErrNotAlias
	}
	userHost, rest, _ := strings.Cut(s, ":")
	user, host, hasUser := strings.Cut(userHost, "@")
	if !hasUser {
		host, user = userHost, ""
	}
	if host == "" || len(host) == 1 { // a Windows drive letter is no host
		return Canonicalized{}, ErrNotAlias
	}
	al, ok := resolve(host)
	if !ok || al.HostName == "" || strings.EqualFold(al.HostName, host) {
		return Canonicalized{}, ErrNotAlias
	}
	if user == "" {
		user = orDefaultUser(al.User)
	}
	canonPrefix := user + "@" + strings.ToLower(al.HostName) + ":"
	return Canonicalized{URL: canonPrefix + rest, Alias: host, HostName: strings.ToLower(al.HostName),
		Rule: OwnerScoped(Access{URL: canonPrefix, Via: userHost + ":"}, canonPrefix+rest)}, nil
}

// orDefaultUser is u, or DefaultSSHUser when u is empty.
func orDefaultUser(u string) string {
	if u == "" {
		return DefaultSSHUser
	}
	return u
}

// querySuffix is "?<query>" of u ("" without one): lore's ?branch= stays.
func querySuffix(u *url.URL) string {
	if u.RawQuery == "" {
		return ""
	}
	return "?" + u.RawQuery
}

// AccessFix is the exact fix for a machine that cannot reach canonical
// directly but can through via: the config.yaml lines lore reads, and
// the git command that does the same for every program on the machine.
type AccessFix struct {
	// YAML is the `access:` block to add to ~/.lore/config.yaml.
	YAML string
	// GitCommand is the global git config alternative.
	GitCommand string
}

// FixFor is the AccessFix mapping rule.URL to rule.Via.
func FixFor(rule Access) AccessFix {
	return AccessFix{
		YAML:       AccessKey + ":\n  - url: " + quoteYAML(rule.URL) + "\n    via: " + quoteYAML(rule.Via),
		GitCommand: "git config --global url." + shellQuote(rule.Via) + ".insteadOf " + shellQuote(rule.URL),
	}
}

// quoteYAML double-quotes a prefix ending in ':' (a bare `git@h:` would
// read as a mapping key).
func quoteYAML(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

// shellQuote double-quotes s for a POSIX shell when it holds anything but
// the url characters that never need it.
func shellQuote(s string) string {
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@:/._-+=", r))
	}) < 0 {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// HostOf is the host of an ssh url (scp-like or ssh://) in lower case;
// ok false for any other url.
func HostOf(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "ssh" || u.Hostname() == "" {
			return "", false
		}
		return strings.ToLower(u.Hostname()), true
	}
	if strings.HasPrefix(s, "/") || !strings.Contains(s, ":") {
		return "", false
	}
	userHost, _, _ := strings.Cut(s, ":")
	if _, h, found := strings.Cut(userHost, "@"); found {
		userHost = h
	}
	if len(userHost) <= 1 {
		return "", false
	}
	return strings.ToLower(userHost), true
}

// RuleVia is the access rule that reaches the canonical url canon through
// the ssh alias alias, scoped to canon's owner (OwnerScoped): the
// canonical owner prefix (user@host:owner/, ssh://user@host/owner/ or
// https://host/owner/) mapped to the same owner on alias (an https url is
// reached as git@<alias>:owner/). ok false for a local path or file url.
func RuleVia(canon, alias string) (Access, bool) {
	s := strings.TrimSpace(canon)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" {
			return Access{}, false
		}
		switch u.Scheme {
		case "ssh":
			user := ""
			if u.User != nil {
				user = u.User.Username() + "@"
			}
			port := ""
			if p := u.Port(); p != "" {
				port = ":" + p
			}
			return OwnerScoped(Access{URL: "ssh://" + user + u.Host + "/", Via: "ssh://" + user + alias + port + "/"}, s), true
		case "https", "http":
			return OwnerScoped(Access{URL: u.Scheme + "://" + u.Host + "/", Via: DefaultSSHUser + "@" + alias + ":"}, s), true
		}
		return Access{}, false
	}
	if _, ok := HostOf(s); !ok {
		return Access{}, false
	}
	userHost, _, _ := strings.Cut(s, ":")
	user, _, hasUser := strings.Cut(userHost, "@")
	if !hasUser {
		return OwnerScoped(Access{URL: userHost + ":", Via: alias + ":"}, s), true
	}
	return OwnerScoped(Access{URL: userHost + ":", Via: user + "@" + alias + ":"}, s), true
}

// splitRemote splits a remote url into its host prefix (user@host:,
// ssh://user@host[:port]/, https://host/) and the path after it; ok false
// for a local path or a file:// url.
func splitRemote(raw string) (prefix, rest string, ok bool) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "://"); i >= 0 {
		if strings.EqualFold(s[:i], "file") {
			return "", "", false
		}
		after := s[i+3:]
		j := strings.Index(after, "/")
		switch {
		case after == "" || j == 0:
			return "", "", false
		case j < 0:
			return s + "/", "", true
		}
		return s[:i+3+j+1], after[j+1:], true
	}
	if strings.HasPrefix(s, "/") || !strings.Contains(s, ":") {
		return "", "", false
	}
	userHost, rest, _ := strings.Cut(s, ":")
	host := userHost
	if _, h, found := strings.Cut(userHost, "@"); found {
		host = h
	}
	if len(host) <= 1 { // a Windows drive letter is no host
		return "", "", false
	}
	return userHost + ":", rest, true
}

// RemoteHost is the lower-case host of an ssh or https remote url (no
// user, no port); ok false for a local path or file url.
func RemoteHost(raw string) (string, bool) {
	p, _, ok := splitRemote(raw)
	if !ok {
		return "", false
	}
	if _, after, found := strings.Cut(p, "://"); found {
		p = after
	}
	p = strings.TrimRight(p, ":/")
	if _, h, found := strings.Cut(p, "@"); found {
		p = h
	}
	if h, _, found := strings.Cut(p, ":"); found {
		p = h
	}
	return strings.ToLower(p), p != ""
}

// OwnerOf is the owner (org or account) of a remote url: the first path
// segment of git@host:owner/repo, ssh://…/owner/repo or
// https://host/owner/repo; "" when the path has no owner segment.
func OwnerOf(raw string) string {
	_, rest, ok := splitRemote(raw)
	if !ok {
		return ""
	}
	owner, _, found := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
	if !found {
		return ""
	}
	return owner
}

// HostWide reports whether rule maps a whole host (url git@github.com:,
// ssh://git@github.com/, https://github.com/): every repo of the host,
// whatever its owner, goes through via.
func HostWide(rule Access) bool {
	_, rest, ok := splitRemote(rule.URL)
	return ok && strings.Trim(rest, "/") == ""
}

// OwnerScoped is rule narrowed to the owner of remote: a host-wide rule
// git@github.com: → git@work-gh: becomes git@github.com:acme/ →
// git@work-gh:acme/ for git@github.com:acme/x.git (a remote with no owner
// segment maps only itself). A rule that is not host-wide, or remote that
// does not start with its url, is returned as it is.
func OwnerScoped(rule Access, remote string) Access {
	if !HostWide(rule) || !strings.HasPrefix(remote, rule.URL) {
		return rule
	}
	if owner := OwnerOf(remote); owner != "" {
		return Narrowed(rule, owner)
	}
	rest := strings.TrimPrefix(remote, rule.URL)
	return Access{URL: rule.URL + rest, Via: rule.Via + rest}
}

// Narrowed is the host-wide rule narrowed to owner (both prefixes get
// owner + "/").
func Narrowed(rule Access, owner string) Access {
	return Access{URL: rule.URL + owner + "/", Via: rule.Via + owner + "/"}
}

// AccessFailureNote is what a failed git network call (clone, fetch,
// push) adds when it went through rule: the mapping, where it is set and
// the next step; for a host-wide rule also how to narrow it.
func AccessFailureNote(rule Access) string {
	note := "this git call went through the access rule " + rule.URL + " → " + rule.Via + " (access: in ~/.lore/config.yaml); check that " + rule.Via + " reaches this repo with the account it is for"
	if p, _, ok := splitRemote(rule.Via); ok && strings.HasSuffix(p, ":") {
		note += " (ssh -T " + strings.TrimSuffix(p, ":") + ")"
	}
	note += ", or fix or remove that rule"
	if HostWide(rule) {
		n := Narrowed(rule, "<owner>")
		note += "; the rule maps every repo of that host, so a repo of another account goes through the alias too: narrow it to the owner the alias is for (url " + n.URL + " via " + n.Via + "), `lore doctor --fix` does it for the org stores"
	}
	return note
}
