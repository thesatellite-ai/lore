package probe

// discover.go — the local facts behind the setup page's account discovery
// (owner request 2026-11-06): a gh login's token (memory only, for
// adapters/ghapi) and git's includeIf identities with their folders.
//
//	wire setupdiscover ─▶ GHToken(user)        gh auth token --hostname github.com --user X (stdout → ghapi.Token)
//	                   ─▶ IncludeIdentities()  git config --global --get-regexp includeif.*.path
//	                                           ─▶ git config --file <each> user.name / user.email

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/ghapi"
)

// GHToken reads user's token for github.com from gh (`gh auth token
// --user`, gh 2.40+; no prompt, no network). The token stays in memory:
// the returned Outcome carries a fixed Detail, never gh's output, so a
// failure can be shown without echoing anything gh printed. Classes: ok,
// not_logged_in, tool_missing, timeout, bad_input.
func (p *Prober) GHToken(ctx context.Context, user string) (ghapi.Token, Outcome) {
	raw, o := p.ghTokenRaw(ctx, user)
	if o.Class != ClassOK {
		return ghapi.Token{}, o
	}
	tok, ok := ghapi.NewToken(raw)
	if !ok {
		return ghapi.Token{}, Outcome{Class: ClassFailed, Detail: "gh auth token printed no token", Took: o.Took}
	}
	return tok, o
}

// ghTokenRaw is GHToken's run: user's token as a string (registered as a
// trace secret), for a gh child's hidden GH_TOKEN (GHRepoAs). It never
// leaves this package.
func (p *Prober) ghTokenRaw(ctx context.Context, user string) (string, Outcome) {
	start := time.Now()
	if !reGHName.MatchString(user) {
		return "", Outcome{Class: ClassBadInput, Detail: "not a GitHub user name"}
	}
	cctx, done, ok := p.slot(ctx, p.o.GHTimeout)
	defer done()
	if !ok {
		return "", timedOut(start)
	}
	// The run is hidden from the request's trace (its stdout IS the token,
	// and the exec hook would store it before Secret below could register
	// it); one Call with the output withheld is recorded instead.
	spec := cmdSpec{bin: ghBin, args: []string{"auth", "token", "--hostname", GitHubHost, "--user", user}, env: ghEnv}
	r := p.run(context.WithValue(cctx, traceKey{}, (*Trace)(nil)), spec)
	took := time.Since(start)
	Secret(ctx, r.stdout)
	if t := TraceFrom(ctx); t != nil {
		c := Call{Kind: CallExec, Label: labelOf(ctx), Command: p.displayCommand(spec), Exit: r.exit, TookMS: took.Milliseconds(),
			TimedOut: r.timedOut, Missing: r.missing, Start: start}
		if r.stdout != "" {
			c.Output = tokenWithheld
		}
		t.add(c)
	}
	switch {
	case r.missing:
		return "", Outcome{Class: ClassToolMissing, Detail: ghBin + " not found on PATH", Took: took}
	case r.timedOut:
		return "", Outcome{Class: ClassTimeout, Detail: "gh auth token timed out", Took: took}
	case !r.ok():
		return "", Outcome{Class: ClassNotLoggedIn, Detail: "gh has no token for " + user, Took: took}
	}
	return strings.TrimSpace(r.stdout), Outcome{Class: ClassOK, Took: took}
}

// tokenWithheld is the traced output of gh auth token (never its stdout).
const tokenWithheld = "(a token: not shown)"

// IncludeIdentity is one conditional include of the global git config
// that sets an identity: "[includeIf "gitdir:~/work/acme/"] path =
// ~/.gitconfig-acme" whose file has user.name / user.email.
type IncludeIdentity struct {
	// Condition is the includeIf condition as written ("gitdir:~/work/acme/").
	Condition string `json:"condition"`
	// Folder is the condition's folder ("~/work/acme/"), "" for a
	// condition that is not gitdir: / gitdir/i: (onbranch:, hasconfig:).
	Folder string `json:"folder,omitempty"`
	// Path is the included file as written; Name and Email what it sets.
	Path  string `json:"path"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// reIncludeIf is one `git config --get-regexp` line of an includeIf path.
var reIncludeIf = regexp.MustCompile(`^includeif\.(.+)\.path\s+(.+)$`)

// gitdir conditions (git-config(1) "Conditional includes").
var gitdirPrefixes = []string{"gitdir:", "gitdir/i:"}

// IncludeIdentities lists the global git config's includeIf files that
// set user.name or user.email (files and git only; no network). A missing
// git or no includeIf is an empty list.
func (p *Prober) IncludeIdentities(ctx context.Context) []IncludeIdentity {
	cctx, done, ok := p.slot(ctx, p.o.GitTimeout)
	defer done()
	if !ok {
		return nil
	}
	r := p.run(cctx, p.gitSpec(Route{}, "", "config", "--global", "--get-regexp", `^includeif\..*\.path$`))
	if !r.ok() {
		return nil
	}
	var out []IncludeIdentity
	for l := range strings.SplitSeq(r.stdout, "\n") {
		m := reIncludeIf.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		inc := IncludeIdentity{Condition: m[1], Path: strings.TrimSpace(m[2])}
		for _, pre := range gitdirPrefixes {
			if f, ok := strings.CutPrefix(inc.Condition, pre); ok {
				inc.Folder = f
			}
		}
		file := expandHome(inc.Path, p.o.Home)
		if !filepath.IsAbs(file) {
			file = filepath.Join(p.o.Home, file) // relative to ~/.gitconfig's folder
		}
		ir := p.run(cctx, p.gitSpec(Route{}, "", "config", "--file", file, "--get-regexp", `^user\.(name|email)$`))
		if !ir.ok() {
			continue
		}
		for kl := range strings.SplitSeq(ir.stdout, "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(kl), " ")
			switch k {
			case keyUserName:
				inc.Name = Sanitize(v)
			case keyUserEmail:
				inc.Email = Sanitize(v)
			}
		}
		if inc.Name != "" || inc.Email != "" {
			out = append(out, inc)
		}
	}
	return out
}
