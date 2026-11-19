package orgcfg

// remotealias.go — a code repo's remote under an ssh host alias, resolved
// to the host it stands for (devco D-531).
//
// A person with two GitHub accounts clones work repos as
// git@<alias>:<owner>/<repo>.git, where ~/.ssh/config says
// "Host <alias> / HostName github.com" (and often a
// url."git@<alias>:<owner>/".insteadOf rule rewrites github.com urls to
// it). Store urls already resolved the alias (D-493, gitbackend's
// identity host); a code repo's remote did not, so whoami showed
// <alias>/<owner>/<repo>, no org's `repos: github.com/<owner>/*` matched,
// the identity fell back to the personal one, and the own-pattern offer
// proposed writing the alias pattern into the team's shared file.
//
//	raw remote ─▶ repourl.Normalize ─▶ ssh form? ─┬▶ insteadOf rule maps it back ─▶ host/path
//	                                               └▶ ssh config HostName (gitbackend.IdentityHost)

import (
	"context"
	"slices"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// insteadOfRule is one url.<Base>.insteadOf <From>: git rewrites a url
// starting with From to Base, so a remote written with Base is From's
// repository under another spelling.
type insteadOfRule struct {
	Base, From string
}

// insteadOf key parts as git config --get-regexp prints them (section and
// key lower-cased, the subsection kept as written).
const (
	insteadOfKeyPrefix = "url."
	insteadOfKeySuffix = ".insteadof"
)

// insteadOfRules picks the url.<base>.insteadOf lines out of a
// `git config --get-regexp` answer.
func insteadOfRules(lines []string) []insteadOfRule {
	var out []insteadOfRule
	for _, l := range lines {
		key, val, ok := strings.Cut(strings.TrimSpace(l), " ")
		val = strings.TrimSpace(val)
		if !ok || val == "" || !strings.HasPrefix(key, insteadOfKeyPrefix) || !strings.HasSuffix(key, insteadOfKeySuffix) {
			continue
		}
		base := key[len(insteadOfKeyPrefix) : len(key)-len(insteadOfKeySuffix)]
		if base != "" {
			out = append(out, insteadOfRule{Base: base, From: val})
		}
	}
	return out
}

// normalizeRemote is repourl.Normalize for a code repo's remote with an
// ssh host alias resolved: an ssh or scp-form url whose prefix an
// insteadOf rule maps back to another host's url takes that host, else the
// host the person's ssh config connects to (gitbackend.IdentityHost). An
// https url is kept as is (a mirror rule is not an alias, F10).
func normalizeRemote(raw string, rules []insteadOfRule) (string, error) {
	n, err := repourl.Normalize(raw)
	if err != nil || !isSSHRemote(raw) {
		return n, err
	}
	host, path, _ := strings.Cut(n, "/")
	for _, r := range rules {
		rest, ok := strings.CutPrefix(strings.TrimSpace(raw), r.Base)
		if !ok || !isSSHRemote(r.Base) {
			continue
		}
		if m, merr := repourl.Normalize(r.From + rest); merr == nil {
			if mh, mp, _ := strings.Cut(m, "/"); mh != host && mp == path {
				return m, nil
			}
		}
	}
	return CanonicalRemote(n), nil
}

// CanonicalRemote is a normalised remote (host/path) with its host
// resolved through the person's ssh config (gitbackend.IdentityHost), for
// a remote read back already normalised (a store's link row recorded
// before D-531 under the alias spelling).
func CanonicalRemote(norm string) string {
	host, path, ok := strings.Cut(norm, "/")
	if !ok || host == "" {
		return norm
	}
	if h := gitbackend.IdentityHost(host); h != host {
		return h + "/" + path
	}
	return norm
}

// isSSHRemote reports git's ssh url forms: ssh:// (and git+ssh://) or the
// scp-like user@host:path.
func isSSHRemote(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	if strings.HasPrefix(s, "ssh://") || strings.HasPrefix(s, "git+ssh://") {
		return true
	}
	if strings.Contains(s, "://") {
		return false
	}
	colon := strings.Index(s, ":")
	slash := strings.Index(s, "/")
	return colon > 1 && (slash < 0 || colon < slash)
}

// insteadOfKeyPattern selects the url.<base>.insteadOf lines of a git
// config (the rules normalizeRemote maps an alias spelling back with).
const insteadOfKeyPattern = `^url\..*\.insteadof$`

// ResolveRemote is a code repo remote (a raw url, as git config or a flag
// holds it) normalised with its ssh host alias resolved, as Remotes
// normalises every remote of a repo: dir's url.<base>.insteadOf rules
// first, then the ssh config's HostName. dir "" reads the person's global
// config only. Every place that keys or matches a code repo by its remote
// (branch layer refs, `branch land --repo`, `org link --remote`) goes
// through it, so a clone through an alias and one from github.com name
// one repo the same way (devco D-532).
func ResolveRemote(ctx context.Context, dir, raw string) (string, error) {
	args := []string{"config", "--get-regexp", insteadOfKeyPattern}
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	var rules []insteadOfRule
	if out, err := gitRun(ctx, args...); err == nil { // exit 1 = no rule
		rules = insteadOfRules(strings.Split(strings.TrimSpace(out), "\n"))
	}
	return normalizeRemote(raw, rules)
}

// GitRewrite is raw as git itself rewrites it before it connects: the
// longest url.<base>.insteadOf prefix in the person's git config replaced
// by its base (git's own rule), raw (trimmed) when no rule matches or git
// cannot be asked. Two store spellings git rewrites to one target reach
// one repository, whatever the ssh config lore reads names (devco D-680:
// a dotted alias host such as github.com-work that only an insteadOf rule
// routes was "another store").
func GitRewrite(ctx context.Context, raw string) string {
	out, err := gitRun(ctx, "config", "--get-regexp", insteadOfKeyPattern)
	if err != nil { // exit 1 = no rule
		return strings.TrimSpace(raw)
	}
	return rewriteURL(raw, insteadOfRules(strings.Split(strings.TrimSpace(out), "\n")))
}

// rewriteURL applies git's insteadOf rule to raw: the rule with the
// longest matching From wins.
func rewriteURL(raw string, rules []insteadOfRule) string {
	s := strings.TrimSpace(raw)
	out, best := s, 0
	for _, r := range rules {
		if len(r.From) > best && strings.HasPrefix(s, r.From) {
			out, best = r.Base+s[len(r.From):], len(r.From)
		}
	}
	return out
}

// PlainRemote is raw normalised the way lore keyed a code repo before
// D-531/D-532 (repourl.Normalize, the alias host kept), "" when that is the
// same as resolved: the spelling an older branch layer ref or remote-only
// link row may still be keyed by.
func PlainRemote(raw, resolved string) string {
	n, err := repourl.Normalize(raw)
	if err != nil || n == resolved {
		return ""
	}
	return n
}

// aliasSpellings maps each of dir's remotes (as Remotes names it) to the
// plain spellings it had before D-531 resolved its ssh alias: a repo linked
// by its remote alone back then holds a link row whose project id came
// from that spelling (devco D-532).
func aliasSpellings(ctx context.Context, dir string) map[string][]string {
	out, err := gitRun(ctx, "-C", dir, "config", "--get-regexp", rawRemoteKeyPattern)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	rules := insteadOfRules(lines)
	spell := map[string][]string{}
	for _, line := range lines {
		key, val, ok := strings.Cut(line, " ")
		val = strings.TrimSpace(val)
		if !ok || val == "" || !strings.HasPrefix(key, "remote.") {
			continue
		}
		n, err := normalizeRemote(val, rules)
		if err != nil {
			continue
		}
		if p := PlainRemote(val, n); p != "" && !slices.Contains(spell[n], p) {
			spell[n] = append(spell[n], p)
		}
	}
	return spell
}
