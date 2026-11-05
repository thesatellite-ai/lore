package host

// trace.go — `--trace` / LORE_TRACE=1 (CLI_REFERENCE 1.1, v0.1.17
// live_doctor.go tracingGit): every git command lore runs for live rows is
// printed to stderr with its duration and outcome. Arguments are git ref
// and path names, never row content (that goes through stdin and files),
// so nothing secret is printed.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// EnvTrace is v0.1.17's trace switch: "1" turns tracing on like --trace.
const EnvTrace = "LORE_TRACE"

// FlagTrace is the root flag that turns tracing on for one command.
const FlagTrace = "trace"

// traceOK is the outcome of a git command that succeeded.
const traceOK = "ok"

// TraceGit is the git runner of live rows with tracing: when On reports
// true at call time, each command is printed to W after it ran. On is
// read per call because the root flag is parsed after the runner is built.
type TraceGit struct {
	Inner gitref.Git
	On    func() bool
	W     io.Writer
}

var _ gitref.Git = TraceGit{}

// Run implements gitref.Git.
func (g TraceGit) Run(ctx context.Context, inv gitref.Invocation) ([]byte, error) {
	if g.On == nil || !g.On() {
		return g.Inner.Run(ctx, inv)
	}
	start := time.Now()
	out, err := g.Inner.Run(ctx, inv)
	status := traceOK
	if err != nil {
		status = "error"
		if code, ok := gitref.ExitCode(err); ok {
			status = fmt.Sprintf("exit %d", code)
		}
	}
	_, _ = fmt.Fprintf(g.W, "trace: git %s (%s, %s)\n", strings.Join(inv.Args, " "), time.Since(start).Round(time.Millisecond), status) // stderr gone: nothing left to report to
	return out, err
}

// TraceOn reports whether LORE_TRACE=1 or flag (the root --trace) is set.
func TraceOn(flag *bool) func() bool {
	return func() bool { return (flag != nil && *flag) || os.Getenv(EnvTrace) == envOn }
}
