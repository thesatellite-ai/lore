package probe

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// Default timeouts and concurrency (ONB 6.1: "at most 4 probes at once
// machine-wide, each with a timeout: ssh and git 20 s, gh 10 s"). The
// caller overrides each through Options (doctor.probe_timeout_s, ONB 7.3).
const (
	DefaultGitTimeout = 20 * time.Second
	DefaultSSHTimeout = 20 * time.Second
	DefaultGHTimeout  = 10 * time.Second
	// DefaultVersionTimeout bounds a harmless "--version" call of a
	// secrets manager (ONB 4.8): such a call answers in well under a second
	// unless the program is broken.
	DefaultVersionTimeout = 5 * time.Second
	// DefaultConcurrency is the machine-wide probe limit (ONB 6.1: a doctor
	// run never floods ssh).
	DefaultConcurrency = 4
	// SSHConnectTimeoutSeconds is ssh's own ConnectTimeout: a host alias
	// that does not answer fails in 5 s instead of holding a probe slot
	// (ONB 4.3 named 10; lowered for the setup page's checks). The probe's
	// timeout still bounds the whole call.
	SSHConnectTimeoutSeconds = "5"
	// waitDelay bounds how long a killed child's pipes may stay open.
	waitDelay = time.Second
	// maxDetail caps the one sanitised stderr line a result carries.
	maxDetail = 200
)

// Options configures a Prober. Zero values are the defaults.
type Options struct {
	// Home is the home folder probes read (~/.ssh, tool folders) and the
	// HOME the child programs see. "" means os.UserHomeDir().
	Home string
	// TmpDir holds the write test's temp bare repo (~/.lore/tmp in
	// production, ONB 4.4). "" means os.TempDir().
	TmpDir string
	// Env is the child programs' base environment (os.Environ(), read at
	// each run, when nil).
	// Probe-specific variables (GIT_TERMINAL_PROMPT, GIT_SSH_COMMAND, HOME…)
	// are set on top.
	Env []string
	// LookPath finds a program (exec.LookPath when nil).
	LookPath func(string) (string, error)
	// Timeouts per probe kind (the Default* constants when 0).
	GitTimeout, SSHTimeout, GHTimeout, VersionTimeout time.Duration
	// Limiter bounds concurrent probes. nil means a new limiter of
	// DefaultConcurrency for this Prober; share one Prober (or one Limiter)
	// per process so the limit holds machine-wide.
	Limiter *Limiter
}

// Prober runs the probes. Safe for concurrent use.
type Prober struct {
	o Options
	// sshCfg caches whether the person's git config sets core.sshCommand
	// (personHasSSHCommand); a pointer so the Prober stays copyable.
	sshCfg *sshConfigOnce
}

// sshConfigOnce is the once-per-Prober read of core.sshCommand.
type sshConfigOnce struct {
	once sync.Once
	set  bool
}

// New returns a Prober with o's defaults filled in.
func New(o Options) *Prober {
	if o.Home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			o.Home = h
		}
	}
	if o.TmpDir == "" {
		o.TmpDir = os.TempDir()
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	o.GitTimeout = orDefault(o.GitTimeout, DefaultGitTimeout)
	o.SSHTimeout = orDefault(o.SSHTimeout, DefaultSSHTimeout)
	o.GHTimeout = orDefault(o.GHTimeout, DefaultGHTimeout)
	o.VersionTimeout = orDefault(o.VersionTimeout, DefaultVersionTimeout)
	if o.Limiter == nil {
		o.Limiter = NewLimiter(DefaultConcurrency)
	}
	return &Prober{o: o, sshCfg: &sshConfigOnce{}}
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// Home returns the home folder the Prober reads.
func (p *Prober) Home() string { return p.o.Home }

// Limiter is a counting semaphore over probes (ONB 6.1).
type Limiter struct {
	ch chan struct{}
}

// NewLimiter returns a limiter admitting n probes at once (n < 1 means 1).
func NewLimiter(n int) *Limiter {
	return &Limiter{ch: make(chan struct{}, max(n, 1))}
}

// acquire waits for a slot; it fails when ctx ends first.
func (l *Limiter) acquire(ctx context.Context) error {
	select {
	case l.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Limiter) release() { <-l.ch }

// slot takes a limiter slot and starts the probe's timeout. The returned
// done must be called; ok is false when the wait for a slot outlived ctx.
func (p *Prober) slot(ctx context.Context, timeout time.Duration) (context.Context, func(), bool) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	if err := p.o.Limiter.acquire(cctx); err != nil {
		return cctx, cancel, false
	}
	return cctx, func() { p.o.Limiter.release(); cancel() }, true
}

// execResult is one child run. stderr is kept only for classification;
// results expose at most a sanitised Detail line of it.
type execResult struct {
	stdout, stderr string
	exit           int
	timedOut       bool
	missing        bool // the program is not on PATH
	startErr       bool // the program could not start
}

// ok reports a clean exit.
func (r execResult) ok() bool { return !r.timedOut && !r.missing && !r.startErr && r.exit == 0 }

// cmdSpec is one child to run.
type cmdSpec struct {
	bin  string
	args []string
	dir  string
	env  []string // KEY=value pairs set on top of the base env
	// hiddenEnv is set after env and never shown: displayCommand and the
	// trace leave it out (it carries a token, e.g. GH_TOKEN for a gh call
	// made as one account).
	hiddenEnv []string
	stdin     string
}

// run executes spec under ctx (the probe's deadline). stdin is closed (or
// fed spec.stdin), stdout and stderr are captured, and the whole process
// tree is killed when ctx ends.
func (p *Prober) run(ctx context.Context, spec cmdSpec) (res execResult) {
	start := time.Now()
	defer func() { p.traceExec(ctx, spec, res, start) }() // trace.go: a no-op unless ctx carries a Trace
	exe, err := p.o.LookPath(spec.bin)
	if err != nil {
		return execResult{missing: true, exit: -1}
	}
	// Built directly (as adapters/secretmgr does): exe is the absolute path
	// LookPath resolved for a fixed program name, every argument is its own
	// argv entry (no shell parses it), and runKillable kills it on timeout.
	cmd := &exec.Cmd{Path: exe, Args: append([]string{exe}, spec.args...), Dir: spec.dir, WaitDelay: waitDelay}
	cmd.Env = p.env(append(slices.Clip(spec.env), spec.hiddenEnv...))
	cmd.Stdin = strings.NewReader(spec.stdin)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err = runKillable(ctx, cmd)
	res = execResult{stdout: out.String(), stderr: stderr.String()}
	switch {
	case ctx.Err() != nil:
		res.timedOut = true
		res.exit = -1
	case err == nil:
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.exit = ee.ExitCode()
		} else {
			res.startErr = true
			res.exit = -1
		}
	}
	return res
}

// env returns the base environment with HOME and extra set on top (a later
// entry of the same key replaces an earlier one).
func (p *Prober) env(extra []string) []string {
	set := map[string]string{}
	var order []string
	add := func(kv string) {
		k, _, _ := strings.Cut(kv, "=")
		if _, seen := set[k]; !seen {
			order = append(order, k)
		}
		set[k] = kv
	}
	base := p.baseEnv()
	// The probes run git with -C or Dir; an inherited GIT_DIR (a git hook
	// exports it) would point them at another repository (logic/gitenv).
	base = gitenv.Clean(base)
	for _, kv := range base {
		add(kv)
	}
	if p.o.Home != "" {
		add(envHome + "=" + p.o.Home)
	}
	for _, kv := range extra {
		add(kv)
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, set[k])
	}
	return out
}

// baseEnv is the environment every child starts from: Options.Env, else
// the process's own.
func (p *Prober) baseEnv() []string {
	if p.o.Env == nil {
		return os.Environ()
	}
	return p.o.Env
}

// runKillable starts cmd and kills its process tree when ctx ends first.
func runKillable(ctx context.Context, cmd *exec.Cmd) error {
	startGroup(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		killTree(cmd)
		<-done
		return ctx.Err()
	}
}

// Environment variable names the probes set.
const (
	envHome           = "HOME"
	envGitPrompt      = "GIT_TERMINAL_PROMPT"
	envGitSSHCommand  = "GIT_SSH_COMMAND"
	envGitSSH         = "GIT_SSH"
	envLCAll          = "LC_ALL"
	envGHPrompt       = "GH_PROMPT_DISABLED"
	envGHNoUpdate     = "GH_NO_UPDATE_NOTIFIER"
	envNoColor        = "NO_COLOR"
	envGCMInteractive = "GCM_INTERACTIVE"
)
