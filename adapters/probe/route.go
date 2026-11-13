package probe

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
)

// GitHubHost is the host whose urls a setup's alias rewrites (Q5: the
// committed .lore/stores.yaml and lore-org.yaml keep the plain github.com url).
const GitHubHost = "github.com"

// sshUser is the login user GitHub (and every git host) expects over ssh.
const sshUser = "git"

// Route is how a setup reaches a git host (Q5, ONB 4.3). Zero Route means
// "ssh's own config decides" (the plain url, the default key).
type Route struct {
	// HostAlias is a ~/.ssh/config Host whose HostName is github.com
	// ("github-acme"). When set, lore's git calls rewrite git@github.com:
	// and ssh://git@github.com/ to it per call, never in git config.
	HostAlias string
	// SSHKey is a key path (private or .pub; ssh -i takes either form and
	// lore never reads the file). Used only when HostAlias is empty: the
	// GIT_SSH_COMMAND fallback of Q5.
	SSHKey string
	// NoAgent adds IdentityAgent=none with the SSHKey fallback, so only the
	// key file itself is tried, never a key the agent holds ("Test all
	// keys": which account does this one file log in as).
	NoAgent bool
	// HTTPSUser is a GitHub login: when set, git reaches github.com over
	// https as that login instead of ssh (the setup's route https, used
	// when no ssh route logs in as the account). GitArgs then rewrite the
	// ssh url forms to https://github.com/ and add a per-call credential
	// helper that runs `gh auth token --hostname github.com --user LOGIN`
	// at each request: the token stays in memory, never in git config, a
	// url or a file.
	HTTPSUser string
}

// reGitHubLogin is a GitHub login (alphanumerics and single hyphens,
// at most 39): it is pasted into the credential helper's shell line.
var reGitHubLogin = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)

// reHostToken is what an alias or host may hold: it becomes an ssh
// argument and part of a git config key, so nothing a shell, ssh or git
// could read as an option or a separator.
var reHostToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Validate refuses an alias or key path that could be read as an option or
// break the quoting of GIT_SSH_COMMAND.
func (r Route) Validate() error {
	if r.HostAlias != "" && !reHostToken.MatchString(r.HostAlias) {
		return errs.New(errs.InvalidInput, "host alias "+Sanitize(r.HostAlias)+" may hold only letters, digits, '.', '_' and '-'").
			WithHint("use the Host name from ~/.ssh/config, for example github-acme")
	}
	if r.HTTPSUser != "" && !reGitHubLogin.MatchString(r.HTTPSUser) {
		return errs.New(errs.InvalidInput, "GitHub login "+Sanitize(r.HTTPSUser)+" may hold only letters, digits and '-'").
			WithHint("use the login gh auth status shows")
	}
	if r.SSHKey != "" && (strings.HasPrefix(r.SSHKey, "-") || strings.ContainsAny(r.SSHKey, "\n\r\x00")) {
		return errs.New(errs.InvalidInput, "ssh key path may not start with '-' or hold a line break").
			WithHint("give the key's file path, for example ~/.ssh/id_acme")
	}
	return nil
}

// sshHost is the host ssh -T connects to: the alias, else github.com.
func (r Route) sshHost() string {
	if r.HostAlias != "" {
		return r.HostAlias
	}
	return GitHubHost
}

// GitArgs are the global git options of Q5 (a): placed before the git
// subcommand, they rewrite github.com urls to the alias for this one call.
// Empty without an alias.
func (r Route) GitArgs() []string {
	if r.HTTPSUser != "" {
		return r.httpsArgs()
	}
	if r.HostAlias == "" {
		return nil
	}
	return []string{
		"-c", "url." + sshUser + "@" + r.HostAlias + ":.insteadOf=" + sshUser + "@" + GitHubHost + ":",
		"-c", "url.ssh://" + sshUser + "@" + r.HostAlias + "/.insteadOf=ssh://" + sshUser + "@" + GitHubHost + "/",
	}
}

// httpsBase is the https url prefix of github.com repos.
const httpsBase = "https://" + GitHubHost + "/"

// httpsArgs are the per-call options of the https route: the scp-like and
// ssh:// forms of github.com rewritten to https, every credential helper
// from the person's git config dropped for this call (an empty
// credential.helper resets the list), and one helper that asks gh for
// HTTPSUser's token. Callers Validate first (the login is pasted into the
// helper's shell line; reGitHubLogin admits no shell character).
func (r Route) httpsArgs() []string {
	helper := "!f() { test \"$1\" = get || exit 0; echo username=x-access-token; echo \"password=$(gh auth token --hostname " +
		GitHubHost + " --user " + r.HTTPSUser + ")\"; }; f"
	return []string{
		"-c", "url." + httpsBase + ".insteadOf=" + sshUser + "@" + GitHubHost + ":",
		"-c", "url." + httpsBase + ".insteadOf=ssh://" + sshUser + "@" + GitHubHost + "/",
		"-c", "credential.helper=",
		"-c", "credential." + httpsBase[:len(httpsBase)-1] + ".helper=" + helper,
	}
}

// sshOptions are the options every probe's ssh gets: never prompt (a
// passphrase or host-key question would hang until the timeout), a short
// connect timeout, and with the Q5 (c) fallback the key alone.
func (r Route) sshOptions(home string) []string {
	opts := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=" + SSHConnectTimeoutSeconds}
	if r.HostAlias == "" && r.SSHKey != "" {
		// Forward slashes: git runs GIT_SSH_COMMAND through sh, and ssh
		// reads C:/Users/... on Windows as it reads C:\Users\....
		opts = append(opts, "-i", filepath.ToSlash(identityPath(expandHome(r.SSHKey, home))), "-o", "IdentitiesOnly=yes")
		if r.NoAgent {
			opts = append(opts, "-o", "IdentityAgent=none")
		}
	}
	return opts
}

// identityPath is the file ssh -i gets: for a recorded .pub, the private
// key beside it when that file exists. ssh -i with a .pub alone signs only
// through an agent holding the key, so without one GitHub refused a key
// that works (realmac finding 6). lore names the path; ssh reads the key.
func identityPath(key string) string {
	if priv, ok := strings.CutSuffix(key, pubSuffix); ok {
		if st, err := os.Stat(priv); err == nil && st.Mode().IsRegular() {
			return priv
		}
	}
	return key
}

// needsOwnSSH reports whether this route only works through lore's own
// ssh line: the key fallback of Q5 (c) passes the key with -i, which a
// person's own ssh command would not do.
func (r Route) needsOwnSSH() bool {
	return r.HTTPSUser == "" && r.HostAlias == "" && r.SSHKey != ""
}

// GitSSHCommand is the GIT_SSH_COMMAND value for this route (git runs it
// through a shell, so every word is single-quoted).
func (r Route) GitSSHCommand(home string) string {
	words := append([]string{"ssh"}, r.sshOptions(home)...)
	for i, w := range words {
		words[i] = shellQuote(w)
	}
	return strings.Join(words, " ")
}

// reShellSafe is a word sh reads literally without quotes.
var reShellSafe = regexp.MustCompile(`^[A-Za-z0-9_./=:@-]+$`)

// shellQuote single-quotes w for sh (GIT_SSH_COMMAND is run by a shell).
func shellQuote(w string) string {
	if w != "" && reShellSafe.MatchString(w) {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}

// expandHome turns a leading ~/ into home, joined with the system's
// separator: on Windows a "/" glued to `C:\Users\a` gave a path that no
// longer equalled the same file found by listing ~/.ssh.
func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok && home != "" {
		return filepath.Join(home, filepath.FromSlash(rest))
	}
	return p
}

// cleanURL returns the url a probe may use: credentials refused (ONB 4.4
// store.url: "a token inside a url is never stored", so never used either),
// a plain https user dropped, and nothing git could read as an option.
func cleanURL(raw string) (string, *Outcome) {
	clean, removed, err := repourl.Strip(raw)
	switch {
	case err != nil:
		return "", &Outcome{Class: ClassBadInput, Detail: "not a git url (a user:token@ part, or unreadable)"}
	case removed.Warn():
		return "", &Outcome{Class: ClassBadInput, Detail: "the url carries a credential; lore never uses a token inside a url: " + Sanitize(clean)}
	case strings.HasPrefix(clean, "-"):
		return "", &Outcome{Class: ClassBadInput, Detail: "a url may not start with '-'"}
	}
	return clean, nil
}
