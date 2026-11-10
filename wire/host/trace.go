package host

// trace.go — `--trace` / LORE_TRACE=1 (CLI_REFERENCE 1.1, v0.1.17
// live_doctor.go tracingGit): every git command lore runs for live rows,
// and every network call to a store (gitref.WithTracer, issue 15), is
// printed to stderr with its duration and outcome. Arguments are git ref
// and path names, never row content (that goes through stdin and files),
// so nothing secret is printed.

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// EnvTrace is v0.1.17's trace switch: "1" turns tracing on like --trace.
const EnvTrace = "LORE_TRACE"

// FlagTrace is the root flag that turns tracing on for one command.
const FlagTrace = "trace"

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
	if g.On == nil || !g.On() || gitref.IsMarkedTraced(ctx) {
		return g.Inner.Run(ctx, inv)
	}
	start := time.Now()
	out, err := g.Inner.Run(ctx, inv)
	_, _ = fmt.Fprintln(g.W, gitref.TraceLine(gitref.TraceCall{Args: inv.Args, Took: time.Since(start), Err: err})) // stderr gone: nothing left to report to
	return out, err
}

// Tracer is the gitref.Tracer that --trace puts in the command's context
// (cmd/lore rootflags.go): the git calls that do not run through TraceGit
// (org link reads and writes, store clones, a linked store's signals
// remote; issue 15) print the same line to w.
func Tracer(w io.Writer) gitref.Tracer {
	return func(c gitref.TraceCall) {
		_, _ = fmt.Fprintln(w, gitref.TraceLine(c)) // stderr gone: nothing left to report to
	}
}

// TraceOn reports whether LORE_TRACE=1 or flag (the root --trace) is set.
func TraceOn(flag *bool) func() bool {
	return func() bool { return (flag != nil && *flag) || os.Getenv(EnvTrace) == envOn }
}
