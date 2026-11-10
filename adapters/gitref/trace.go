package gitref

// trace.go — the one `--trace` line format (CLI_REFERENCE 1.1, v0.1.17
// tracingGit) and the context seam that carries a tracer to git calls
// that do not go through a Git runner (issue 15: the org link reads and
// writes, the store clone backend, a linked store's signals remote).
//
//	cmd/lore --trace ─▶ WithTracer(ctx, f) ─▶ any package's git call:
//	                                          TracerFrom(ctx)(TraceCall{…})
//	f(c) ─▶ TraceLine(c):
//	   trace: git <args> (<took>, ok|exit N|error[, remote <host>][, env <NAMES>])
//
// The context carries the tracer the way net/http/httptrace carries a
// ClientTrace: a per-request hook, not package state, so a command run
// without --trace (or the web server) pays one context lookup and prints
// nothing. Remote is the url the call reaches, before the call's own
// `-c url.<base>.insteadOf=<prefix>` options; TraceLine applies them, so
// the line names the host git actually connected to (the setup's ssh
// alias). Only host names are printed: a url's credentials are masked in
// the arguments, env values are never printed (only their names).

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// TraceCall is one finished git command, as a tracer receives it.
type TraceCall struct {
	// Args are git's arguments, without the program name.
	Args []string
	// Remote is the url a network call reaches, as the repo's config spells
	// it (before Args' insteadOf options); "" for a local command or when
	// it is not known.
	Remote string
	// Env holds the extra KEY=VALUE entries the command ran with; only the
	// names are ever printed.
	Env []string
	// Took is how long the command ran.
	Took time.Duration
	// Err is the command's error; nil when it succeeded.
	Err error
}

// Tracer receives every traced git command after it ran. It must be safe
// for concurrent use (calls may come from several goroutines).
type Tracer func(TraceCall)

// tracerKey is the context key of the tracer.
type tracerKey struct{}

// WithTracer returns ctx carrying f; a nil f returns ctx unchanged.
func WithTracer(ctx context.Context, f Tracer) context.Context {
	if f == nil {
		return ctx
	}
	return context.WithValue(ctx, tracerKey{}, f)
}

// TracerFrom returns the tracer ctx carries, nil when none.
func TracerFrom(ctx context.Context) Tracer {
	f, _ := ctx.Value(tracerKey{}).(Tracer)
	return f
}

// tracedKey marks a context whose git call its caller traces itself.
type tracedKey struct{}

// MarkTraced returns ctx marked as traced by the caller: a tracing Git
// runner (wire/host TraceGit) skips the call, so a gitremote network call
// made through a traced repo prints one line (with its remote), not two.
func MarkTraced(ctx context.Context) context.Context {
	return context.WithValue(ctx, tracedKey{}, true)
}

// IsMarkedTraced reports whether ctx came from MarkTraced.
func IsMarkedTraced(ctx context.Context) bool {
	v, _ := ctx.Value(tracedKey{}).(bool)
	return v
}

// TraceOK is the outcome of a git command that succeeded.
const TraceOK = "ok"

// traceError is the outcome of a command that failed without an exit status
// (git missing, the context cancelled).
const traceError = "error"

// TraceLine renders c as the one trace line (no trailing newline).
func TraceLine(c TraceCall) string {
	var b strings.Builder
	masked := make([]string, len(c.Args))
	for i, a := range c.Args {
		masked[i] = maskURLCredentials(a)
	}
	fmt.Fprintf(&b, "trace: git %s (%s, %s", strings.Join(masked, " "), c.Took.Round(time.Millisecond), traceStatus(c.Err))
	if c.Remote != "" {
		if h := remoteHost(rewriteInsteadOf(c.Remote, c.Args)); h != "" {
			b.WriteString(", remote " + h)
		}
	}
	if names := envNames(c.Env); len(names) > 0 {
		b.WriteString(", env " + strings.Join(names, ","))
	}
	b.WriteString(")")
	return b.String()
}

// exitCoder is an error that carries a process exit status
// (*exec.ExitError, and other packages' git errors that expose one).
type exitCoder interface{ ExitCode() int }

// traceStatus is ok, "exit N" or error for err.
func traceStatus(err error) string {
	if err == nil {
		return TraceOK
	}
	if code, ok := ExitCode(err); ok {
		return fmt.Sprintf("exit %d", code)
	}
	var ec exitCoder
	if errors.As(err, &ec) && ec.ExitCode() >= 0 {
		return fmt.Sprintf("exit %d", ec.ExitCode())
	}
	return traceError
}

// insteadOfKey is the git config key suffix of a url rewrite
// (`url.<base>.insteadOf=<prefix>`).
const insteadOfKey = ".insteadof="

// urlKeyPrefix starts every url rewrite config key.
const urlKeyPrefix = "url."

// rewriteInsteadOf applies the `-c url.<base>.insteadOf=<prefix>` options
// in args to raw the way git does: the longest matching prefix wins.
func rewriteInsteadOf(raw string, args []string) string {
	best, base := "", ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "-c" {
			continue
		}
		kv := args[i+1]
		low := strings.ToLower(kv)
		j := strings.Index(low, insteadOfKey)
		if !strings.HasPrefix(low, urlKeyPrefix) || j < 0 {
			continue
		}
		prefix := kv[j+len(insteadOfKey):]
		if strings.HasPrefix(raw, prefix) && len(prefix) > len(best) {
			best, base = prefix, kv[len(urlKeyPrefix):j]
		}
	}
	if best == "" {
		return raw
	}
	return base + strings.TrimPrefix(raw, best)
}

// remoteHost is the host part of a remote url, never its credentials or
// path: "git@github-work" for scp-like git@github-work:acme/hub.git,
// "github.com" for https://github.com/acme/hub.git ("ssh://git@host:22"
// keeps the user and port). "" for a local path or an unparsable url.
func remoteHost(raw string) string {
	u := strings.TrimSpace(raw)
	switch {
	case strings.Contains(u, "://"):
		p, err := url.Parse(u)
		if err != nil || p.Host == "" {
			return ""
		}
		if p.Scheme == "ssh" && p.User != nil {
			return p.User.Username() + "@" + p.Host
		}
		return p.Host
	case strings.Contains(u, ":") && !strings.HasPrefix(u, "/"):
		h, _, _ := strings.Cut(u, ":")
		return h
	}
	return ""
}

// credentialURL finds the userinfo of a url inside an argument
// (scheme://userinfo@).
var credentialURL = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)([^/@\s]+)@`)

// maskURLCredentials replaces the userinfo of every url in a with "***",
// except a bare ssh user name (ssh://git@host), which is not a secret. A
// token in an https url (https://<token>@github.com) is the case it exists
// for.
func maskURLCredentials(a string) string {
	return credentialURL.ReplaceAllStringFunc(a, func(m string) string {
		sub := credentialURL.FindStringSubmatch(m)
		scheme, user := sub[1], sub[2]
		if strings.HasSuffix(strings.ToLower(scheme), "ssh://") && !strings.Contains(user, ":") {
			return m
		}
		return scheme + "***@"
	})
}

// envNames is the KEY of every KEY=VALUE entry, in order.
func envNames(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && k != "" {
			out = append(out, k)
		}
	}
	return out
}
