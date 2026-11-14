package gitremote

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// run runs one network call of g (Fetch, List, Push all come here). With a
// tracer in ctx (--trace, gitref.WithTracer) it is printed with the host it
// reached: Name's url is read from the repo's config only then, so a run
// without --trace costs nothing extra (issue 15).
func (g *Git) run(ctx context.Context, args []string) ([]byte, error) {
	out, err := g.runOnce(ctx, args)
	if err == nil || !neverConnected(err) {
		return out, err
	}
	// L-12 / L-10: one dropped connection (a flaky link, a timed-out ssh
	// handshake) failed the whole claim or sync. The connection never
	// opened, so the remote saw nothing and a second try is safe even for
	// a push or a lease; one retry only, after a short pause, so a remote
	// that is really down still fails fast with git's own words.
	select {
	case <-ctx.Done():
		return out, err
	case <-time.After(RetryPause):
	}
	return g.runOnce(ctx, args)
}

// RetryPause is the wait before the one retry of a call that never
// connected (run here, and the store clones' runner, which shares
// NeverConnected).
const RetryPause = 300 * time.Millisecond

// connectFailures are git's and ssh's words (lower case, LC_ALL=C) for a
// call whose connection never opened: nothing reached the remote. "connection
// reset" and "closed by remote" are left out on purpose: those can come
// after the remote applied a push.
var connectFailures = []string{"ssh: connect to host", "could not resolve host", "could not resolve hostname",
	"connection refused", "connection timed out", "network is unreachable", "no route to host",
	"temporary failure in name resolution", "failed to connect to"}

// neverConnected reports whether err is git exiting because its
// connection to the remote never opened (connectFailures in its stderr).
func neverConnected(err error) bool {
	if _, isExit := gitref.ExitCode(err); !isExit {
		return false
	}
	return NeverConnected(gitref.Stderr(err))
}

// NeverConnected reports whether git's stderr of a failed remote call says
// the connection never opened (connectFailures): the remote saw nothing, so
// one more try is safe for any call, a push included.
func NeverConnected(stderr string) bool {
	low := strings.ToLower(stderr)
	for _, m := range connectFailures {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// runOnce is one try of run.
func (g *Git) runOnce(ctx context.Context, args []string) ([]byte, error) {
	inv := g.invocation(args)
	trace := gitref.TracerFrom(ctx)
	if trace == nil {
		return g.Repo.Run(ctx, inv)
	}
	ctx = gitref.MarkTraced(ctx) // traced here, once, with its remote
	start := time.Now()
	out, err := g.Repo.Run(ctx, inv)
	trace(gitref.TraceCall{Args: inv.Args, Remote: g.remoteURL(ctx), Env: inv.Env, Took: time.Since(start), Err: err})
	return out, err
}

// remoteURL is the url Name points at: Name itself when it is a url, else
// the configured remote's url ("" when it has none).
func (g *Git) remoteURL(ctx context.Context) string {
	if strings.Contains(g.Name, ":") || strings.HasPrefix(g.Name, "/") {
		return g.Name
	}
	u, err := g.Repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", "remote." + g.Name + ".url"}})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(u))
}

// Fetch implements Remote. It passes `--refmap=` so the remote's configured
// fetch refspecs are not applied as a side effect (git would otherwise
// "opportunistically" update their destinations too — including a forced
// refspec into refs/lore/*), and `--no-tags` so a lore fetch never pulls
// tags the user did not ask for.
func (g *Git) Fetch(ctx context.Context, req FetchRequest) error {
	if len(req.Refspecs) == 0 {
		return fmt.Errorf("%w: no refspecs", ErrBadRequest)
	}
	args := []string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--refmap="}
	if req.Prune {
		args = append(args, "--prune")
	}
	args = append(args, "--", g.Name)
	args = append(args, req.Refspecs...)
	if _, err := g.run(ctx, args); err != nil {
		stderr := gitref.Stderr(err)
		if _, isExit := gitref.ExitCode(err); !isExit {
			return err // git never ran (missing binary, cancelled context)
		}
		if strings.Contains(stderr, msgMissingRemoteRef) {
			return &TransportError{Op: opFetch, Err: ErrRemoteRefMissing, Stderr: stderr}
		}
		return &TransportError{Op: opFetch, Err: ErrUnconfirmed, Stderr: stderr}
	}
	return nil
}

// Operation names used in TransportError.
const (
	opFetch = "fetch"
	opPush  = "push"
	opList  = "ls-remote"
)

// git's message texts this file classifies (the runner forces LC_ALL=C).
const (
	msgMissingRemoteRef = "couldn't find remote ref"
	msgNoAtomic         = "does not support --atomic"
)

// List implements Remote.
func (g *Git) List(ctx context.Context, patterns ...string) (map[string]string, error) {
	args := append([]string{"ls-remote", "--refs", "--", g.Name}, patterns...)
	out, err := g.run(ctx, args)
	if err != nil {
		if _, isExit := gitref.ExitCode(err); !isExit {
			return nil, err
		}
		return nil, &TransportError{Op: opList, Err: ErrUnconfirmed, Stderr: gitref.Stderr(err)}
	}
	refs := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if id, name, ok := strings.Cut(line, "\t"); ok {
			refs[name] = id
		}
	}
	return refs, nil
}

// Push implements Remote. It always runs `git push --porcelain --no-verify`
// (the local pre-push hook is for people pushing code; lore's own plumbing
// pushes of refs/lore/* must not re-enter it) and reads
// one verdict per ref from it; a non-zero exit with every verdict present is
// a normal result (rejections are Status values), while a run that produced
// no verdict for some ref is a *TransportError wrapping ErrUnconfirmed.
func (g *Git) Push(ctx context.Context, req PushRequest) (PushResult, error) {
	if err := validate(req); err != nil {
		return PushResult{}, err
	}
	args := []string{"push", "--porcelain", "--no-verify"}
	if req.Atomic {
		args = append(args, "--atomic")
	}
	var specs []string
	for _, u := range req.Updates {
		if u.Mode == ModeLease {
			args = append(args, "--force-with-lease="+u.Dst+":"+u.Expect)
		}
		specs = append(specs, u.Src+":"+u.Dst)
	}
	args = append(args, "--", g.Name)
	args = append(args, specs...)
	out, err := g.run(ctx, args)
	stderr := gitref.Stderr(err)
	if err != nil {
		if _, isExit := gitref.ExitCode(err); !isExit {
			return PushResult{}, err
		}
		if req.Atomic && strings.Contains(stderr, msgNoAtomic) {
			return PushResult{}, &TransportError{Op: opPush, Err: ErrAtomicUnsupported, Stderr: stderr}
		}
	}
	modes := make(map[string]UpdateMode, len(req.Updates))
	for _, u := range req.Updates {
		modes[u.Dst] = u.Mode
	}
	verdicts := parsePorcelain(string(out), stderr, modes)
	res := PushResult{Refs: make([]RefResult, 0, len(req.Updates))}
	for _, u := range req.Updates {
		v, ok := verdicts[u.Dst]
		if !ok {
			// No verdict for this ref (the remote was unreachable, or git
			// stopped before sending): nothing may be assumed applied.
			return PushResult{}, &TransportError{Op: opPush, Err: ErrUnconfirmed, Stderr: stderr}
		}
		res.Refs = append(res.Refs, v)
	}
	return res, nil
}

// Porcelain flags (git push --porcelain, "Output" in git-push(1)).
const (
	flagFastForward = ' '
	flagForced      = '+'
	flagDeleted     = '-'
	flagNew         = '*'
	flagRejected    = '!'
	flagUpToDate    = '='
)

// porcelainFields is the field count of a porcelain ref line:
// "<flag>\t<from>:<to>\t<summary>".
const porcelainFields = 3

// Rejection reasons git prints in parentheses after "[rejected]" or
// "[remote rejected]".
const (
	reasonStale      = "stale info"
	reasonNonFF      = "non-fast-forward"
	reasonFetchFirst = "fetch first"
	reasonAtomic     = "atomic push failed"
	// reasonAtomicRemote is receive-pack's reason for the refs of an atomic
	// push that were fine while another ref of the same push was refused
	// server-side (hook, protection): nothing was applied, like reasonAtomic.
	reasonAtomicRemote = "atomic push failure"
	summaryRemoteRej   = "[remote rejected]"
	summaryRejected    = "[rejected]"
	porcelainToPrefix  = "To "
)

// parsePorcelain reads `git push --porcelain` stdout into verdicts keyed by
// destination ref. stderr is consulted only to recognise protection
// refusals, whose detail the remote prints there; modes (the update mode
// per destination) turns a race lost on the server into the status the
// caller's mode expects.
func parsePorcelain(out, stderr string, modes map[string]UpdateMode) map[string]RefResult {
	verdicts := map[string]RefResult{}
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" || strings.HasPrefix(line, porcelainToPrefix) {
			continue
		}
		fields := strings.SplitN(line, "\t", porcelainFields)
		if len(fields) != porcelainFields || len(fields[0]) != 1 {
			continue // "Done" and anything else that is not a ref line
		}
		_, dst, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		summary := fields[2]
		v := RefResult{Dst: dst, Message: summary}
		switch fields[0][0] {
		case flagFastForward, flagForced, flagDeleted, flagNew:
			v.Status = StatusOK
		case flagUpToDate:
			v.Status = StatusUpToDate
		case flagRejected:
			v.Status = classifyRejection(summary, stderr, dst, modes[dst])
			if v.Status == StatusProtected {
				v.Message = summary + "; " + protectionLine(stderr, dst)
			}
		default:
			continue
		}
		verdicts[dst] = v
	}
	return verdicts
}

// serverRaceReasons are remote-rejection reasons that mean another push
// moved the ref between this push's own check and the server's ref update:
// the client saw the expected value, the server's compare-and-swap did not
// ("incorrect old value provided" from receive-pack, "cannot lock ref" /
// "failed to lock" when the other push still held the ref lock, "reference
// already exists" when two pushes create the same absent ref at once and the
// loser's expect-absent check passed before the winner's create landed,
// "atomic transaction failed" when an atomic push's server-side ref
// transaction could not take its locks because another push held them; git
// then reports every ref of the losing push with it and applies none,
// "failed to update ref" when receive-pack's non-atomic ref update lost the
// ref lock — Apple Git 2.50 reports a lost race only this way, with the
// "cannot lock ref" detail on stderr alone — and "unable to update local ref",
// the older receive-pack wording for the same lock loss). It is the
// same lost race as a stale lease, only detected one step later, so it must
// be retried like one — never reported as a refusal. Hook declines and
// protection refusals carry their own reasons and never match these.
var serverRaceReasons = []string{"incorrect old value provided", "cannot lock ref", "failed to lock", "reference already exists", "atomic transaction failed", "failed to update ref", "unable to update local ref"}

// classifyRejection maps a "!" line's summary to a Status; mode is the
// update's mode, which decides how a lost race is reported.
func classifyRejection(summary, stderr, dst string, mode UpdateMode) Status {
	reason := ""
	if i := strings.LastIndex(summary, "("); i >= 0 {
		reason = strings.TrimSuffix(summary[i+1:], ")")
	}
	switch {
	case strings.HasPrefix(summary, summaryRemoteRej):
		if isProtection(reason, stderr, dst) {
			return StatusProtected
		}
		for _, r := range serverRaceReasons {
			if strings.Contains(reason, r) {
				if mode == ModeLease {
					return StatusStale
				}
				return StatusNonFastForward
			}
		}
		if reason == reasonAtomicRemote {
			return StatusAtomicFailed
		}
		return StatusRemoteRejected
	case reason == reasonStale:
		return StatusStale
	case reason == reasonAtomic:
		return StatusAtomicFailed
	case reason == reasonNonFF, reason == reasonFetchFirst:
		return StatusNonFastForward
	case strings.HasPrefix(summary, summaryRejected):
		// Other local refusals ("already exists" for tags, …): the remote
		// was not changed and retrying as-is will not help.
		return StatusRemoteRejected
	}
	return StatusRemoteRejected
}

// protectionLine returns the remote's message line about dst's protection,
// trimmed, for the user-facing message.
func protectionLine(stderr, dst string) string {
	for line := range strings.SplitSeq(stderr, "\n") {
		if strings.Contains(line, dst) && hasMarker(line) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
