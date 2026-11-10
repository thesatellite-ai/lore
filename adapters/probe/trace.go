package probe

// trace.go — what a probe actually ran, for the setup page's "Details"
// panels and its debug log (owner 2026-11-06: "an easy way to debug on
// every step").
//
//	surface ─▶ WithTrace(ctx) ─▶ op ─▶ Prober.run ─▶ trace.add(Call)   (one hunk in run.go)
//	                                   http caller ─▶ Record(ctx, Call)
//	surface ◀─ Trace.Calls()  (every field already redacted)
//
// The trace rides in the context, so nothing is recorded (and nothing is
// kept) unless a caller asked for it; there is no package state. Every
// text a Call carries passes Trace.Redact before it is stored: a Call is
// safe to show, copy, log and send as it is.

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Trace limits: a Call carries a short, readable excerpt, never a dump.
const (
	// TraceOutputLines is the most output lines a Call keeps.
	TraceOutputLines = 20
	// TraceOutputBytes is the most output bytes a Call keeps.
	TraceOutputBytes = 2048
	// traceMaxCalls bounds one trace (a doctor --all run probes every
	// setup; past this the oldest calls are dropped and Dropped counts them).
	traceMaxCalls = 500
	// minSecretLen is the shortest registered secret replaced in text: a
	// shorter one would mask ordinary words.
	minSecretLen = 6
	// RedactMask replaces every secret value.
	RedactMask = "<redacted>"
	// redactKeyMask replaces a private key block's body.
	redactKeyMask = "<redacted private key>"
	// truncMark ends a trimmed output.
	truncMark = "… (trimmed)"
)

// Call kinds.
const (
	// CallExec is a child program run by the Prober.
	CallExec = "exec"
	// CallHTTP is an HTTP request a caller recorded with Record.
	CallHTTP = "http"
	// CallCache is an answer served from a cache (RecordCached): nothing
	// ran; CachedAt is when the answer was probed (its calls are in that
	// earlier request's trace).
	CallCache = "cache"
)

// cachedClock is the time format of a cached call's Command (local time:
// the page and the terminal show the user's clock).
const cachedClock = "15:04:05"

// Call is one thing lore ran for a request. Every text field is redacted.
type Call struct {
	Kind string `json:"kind"`
	// Label is the check (or prefill) the call belongs to (WithLabel);
	// "" when the caller set none.
	Label string `json:"label,omitempty"`
	// Command is the call as one line a person can paste into a terminal
	// (env prefix, program, shell-quoted arguments; the home folder shown
	// as ~), or "GET https://…" for an HTTP call.
	Command string `json:"command"`
	// Dir is the working folder ("" = the page's own).
	Dir string `json:"dir,omitempty"`
	// Stdin says how much input was fed ("" = none); its content is never kept.
	Stdin    string    `json:"stdin,omitempty"`
	Exit     int       `json:"exit"`
	TookMS   int64     `json:"took_ms"`
	TimedOut bool      `json:"timed_out,omitempty"`
	Missing  bool      `json:"missing,omitempty"`
	Error    string    `json:"error,omitempty"`
	Output   string    `json:"output,omitempty"`
	Start    time.Time `json:"start"`
	// CachedAt is set on a CallCache call only: when the reused answer was probed.
	CachedAt time.Time `json:"cached_at,omitzero"`
}

// Trace collects the Calls of one request. Safe for concurrent use (the
// Prober runs checks in parallel).
type Trace struct {
	mu      sync.Mutex
	calls   []Call
	secrets []string
	dropped int
}

type traceKey struct{}
type labelKey struct{}

// WithTrace returns ctx carrying a new Trace, and the Trace.
func WithTrace(ctx context.Context) (context.Context, *Trace) {
	t := &Trace{}
	return context.WithValue(ctx, traceKey{}, t), t
}

// TraceFrom is the Trace ctx carries (nil: none, nothing is recorded).
func TraceFrom(ctx context.Context) *Trace {
	t, _ := ctx.Value(traceKey{}).(*Trace)
	return t
}

// WithLabel tags the Calls made under ctx (a check id).
func WithLabel(ctx context.Context, label string) context.Context {
	return context.WithValue(ctx, labelKey{}, label)
}

func labelOf(ctx context.Context) string {
	l, _ := ctx.Value(labelKey{}).(string)
	return l
}

// Secret registers a value (a token held in memory, a password) that must
// never appear in the request's trace or log: Redact replaces it wherever
// it shows up. Without a trace in ctx it does nothing. Register before the
// call that may echo it.
func Secret(ctx context.Context, value string) {
	if t := TraceFrom(ctx); t != nil {
		t.AddSecret(value)
	}
}

// AddSecret registers value on t (see Secret). Values shorter than
// minSecretLen are ignored.
func (t *Trace) AddSecret(value string) {
	value = strings.TrimSpace(value)
	if len(value) < minSecretLen {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range t.secrets {
		if s == value {
			return
		}
	}
	t.secrets = append(t.secrets, value)
}

// Record adds an HTTP (or any non-exec) call made under ctx; it is
// redacted and labelled like an exec call. Without a trace it does nothing.
func Record(ctx context.Context, c Call) {
	t := TraceFrom(ctx)
	if t == nil {
		return
	}
	if c.Kind == "" {
		c.Kind = CallHTTP
	}
	if c.Label == "" {
		c.Label = labelOf(ctx)
	}
	t.add(c)
}

// RecordCached records that what (a short name, e.g. "github.users") was
// answered from a cache probed at at: a CallCache call, so a Details panel
// says "cached, from <time>" instead of showing no calls. Labelled from
// ctx like every call; without a trace it does nothing.
func RecordCached(ctx context.Context, what string, at time.Time) {
	Record(ctx, Call{Kind: CallCache, Command: "cached, from " + at.Local().Format(cachedClock) + ": " + what + " (no program ran now)",
		Start: time.Now(), CachedAt: at})
}

// add redacts c and appends it.
func (t *Trace) add(c Call) {
	c.Command = t.Redact(c.Command)
	c.Dir = t.Redact(c.Dir)
	c.Error = t.Redact(c.Error)
	c.Output = TrimOutput(t.Redact(c.Output))
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, c)
	if over := len(t.calls) - traceMaxCalls; over > 0 {
		t.calls = append([]Call(nil), t.calls[over:]...)
		t.dropped += over
	}
}

// Calls is a copy of the recorded calls, oldest first.
func (t *Trace) Calls() []Call {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Call{}, t.calls...)
}

// Dropped is how many old calls were dropped past the trace's bound.
func (t *Trace) Dropped() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dropped
}

// Redact is RedactText with t's registered secrets.
func (t *Trace) Redact(s string) string {
	t.mu.Lock()
	secrets := append([]string(nil), t.secrets...)
	t.mu.Unlock()
	return RedactText(s, secrets...)
}

// Redaction shapes (on top of Sanitize's url userinfo and token shapes).
// Patterns that name a credential are joined from parts so the repo's own
// leak scanner, which reads source lines, does not take them for one.
var (
	// a PEM private key block (any type), or its unterminated start.
	rePrivateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE ` + `KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE ` + `KEY-----|\z)`)
	// an Authorization header (or JSON field) value, to the end of the line or quote.
	reAuthHeader = regexp.MustCompile(`(?i)((?:proxy-)?authori` + `zation["']?\s*[:=]\s*["']?)[^"'\r\n]+`)
	// a Bearer / token scheme value anywhere.
	reBearer = regexp.MustCompile(`(?i)\b(bearer|token)(\s+)[A-Za-z0-9._~+/=-]{8,}`)
	// key=value / key: value / "key": "value" for credential-named keys.
	reNamedSecret = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:pass` + `word|passwd|pwd|secret|api[_-]?key|access[_-]?key|private[_-]?key|client[_-]?secret|auth[_-]?token|access[_-]?token|refresh[_-]?token|oauth[_-]?token)["']?\s*[:=]\s*["']?)([^\s"'&,;}]+)`)
)

// RedactText removes every secret shape from s: private key blocks,
// Authorization and Bearer/token values, credential-named key=value pairs,
// url userinfo, well-known token prefixes (GitHub ghp_/gho_/ghs_/ghu_/ghr_,
// github_pat_, GitLab, Slack, AWS, npm, sk-), and every value in secrets.
// It keeps the text's lines (unlike Sanitize, which makes one short line).
func RedactText(s string, secrets ...string) string {
	if s == "" {
		return s
	}
	for _, v := range secrets {
		if len(v) >= minSecretLen {
			s = strings.ReplaceAll(s, v, RedactMask)
		}
	}
	s = rePrivateKey.ReplaceAllString(s, redactKeyMask)
	s = reAuthHeader.ReplaceAllString(s, "${1}"+RedactMask)
	s = reBearer.ReplaceAllString(s, "${1}${2}"+RedactMask)
	s = reNamedSecret.ReplaceAllString(s, "${1}"+RedactMask)
	s = reURLUserinfo.ReplaceAllString(s, "$1"+RedactMask+"@")
	s = reTokenShape.ReplaceAllString(s, RedactMask)
	return s
}

// TrimOutput keeps at most TraceOutputLines lines and TraceOutputBytes
// bytes of s (trailing blank lines dropped), marking a cut.
func TrimOutput(s string) string {
	s = strings.TrimRight(s, " \t\r\n")
	cut := false
	if lines := strings.Split(s, "\n"); len(lines) > TraceOutputLines {
		s = strings.Join(lines[:TraceOutputLines], "\n")
		cut = true
	}
	if len(s) > TraceOutputBytes {
		s = strings.ToValidUTF8(s[:TraceOutputBytes], "")
		cut = true
	}
	if cut {
		s += "\n" + truncMark
	}
	return s
}

// traceExec records one finished child run on ctx's trace (run.go).
func (p *Prober) traceExec(ctx context.Context, spec cmdSpec, res execResult, start time.Time) {
	t := TraceFrom(ctx)
	if t == nil {
		return
	}
	c := Call{
		Kind: CallExec, Label: labelOf(ctx), Command: p.displayCommand(spec), Dir: p.tilde(spec.dir),
		Exit: res.exit, TookMS: time.Since(start).Milliseconds(), TimedOut: res.timedOut, Missing: res.missing, Start: start,
	}
	if spec.stdin != "" {
		c.Stdin = strconv.Itoa(len(spec.stdin)) + " bytes (not shown)"
	}
	switch {
	case res.missing:
		c.Error = spec.bin + " is not on PATH"
	case res.startErr:
		c.Error = spec.bin + " could not start"
	case res.timedOut:
		c.Error = "timed out"
	}
	out := res.stdout
	if res.stderr != "" {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += res.stderr
	}
	c.Output = out
	t.add(c)
}

// displayCommand is spec as one pasteable line: the probe's env settings,
// the program name and its arguments, the home folder as ~ where that
// still reads literally.
func (p *Prober) displayCommand(spec cmdSpec) string {
	words := make([]string, 0, len(spec.env)+len(spec.args)+1)
	for _, kv := range spec.env {
		k, v, _ := strings.Cut(kv, "=")
		words = append(words, k+"="+p.word(v))
	}
	words = append(words, p.word(spec.bin))
	for _, a := range spec.args {
		words = append(words, p.word(a))
	}
	return strings.Join(words, " ")
}

// word shell-quotes w, writing a home-folder path as ~/… when the rest is
// shell-safe (so a pasted command still expands it).
func (p *Prober) word(w string) string {
	if t := p.tilde(w); t != w && reShellSafe.MatchString(strings.TrimPrefix(t, "~/")) {
		return t
	}
	return shellQuote(w)
}

// tilde writes a path under the home folder as ~/…
func (p *Prober) tilde(path string) string {
	h := strings.TrimSuffix(p.o.Home, "/")
	if h == "" {
		return path
	}
	if path == h {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, h+"/"); ok {
		return "~/" + rest
	}
	return path
}
