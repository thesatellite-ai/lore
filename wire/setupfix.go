package wire

// setupfix.go — setup.FixRunner on *App (ONBOARDING_DESIGN 2.2 Fix, 5.3):
// runs a Runnable fix's argv after the person confirmed it.
//
//	argv ─▶ argv[0] on FixPrograms? ── no ──▶ E_FIX_REFUSED
//	        │ yes
//	        ▼
//	     exec (no shell, no stdin, FixTimeout) ─▶ exit code + last FixTailLines lines, each probe.Sanitize'd
//
// No shell ever sees the argv, so a value from an answer (a repo name, a
// tool name) can never become a second command.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/ops/team"
)

// Programs a fix may run (5.3 allowlist). Every catalog fix command starts
// with one of them: lore itself (skill install, setup --agent-hooks, init,
// render), gh (repo create/edit, collaborator grant) and git.
const (
	FixProgramLore = "lore"
	FixProgramGH   = "gh"
	FixProgramGit  = "git"
)

// FixPrograms is the allowlist, as a list for messages and tests.
func FixPrograms() []string { return []string{FixProgramLore, FixProgramGH, FixProgramGit} }

const (
	// FixTimeout bounds one fix: gh repo create and lore init finish in
	// seconds; two minutes covers a slow network without hanging the page.
	FixTimeout = 2 * time.Minute
	// FixTailLines is how much output the page shows: enough for gh's or
	// git's error, not a full log.
	FixTailLines = 20
)

// setupFixRunner runs fixes. lore finds the running binary, so a fix
// `lore …` runs this same version, never another lore on PATH.
type setupFixRunner struct {
	lore func() (string, error)
}

var _ opssetup.FixRunner = setupFixRunner{}

// RunFix implements opssetup.FixRunner. A refused program is an error
// (E_FIX_REFUSED); a program that ran and failed is a FixRun with its
// non-zero exit and no error, so the page can show the output.
func (f setupFixRunner) RunFix(ctx context.Context, argv []string) (opssetup.FixRun, error) {
	return f.RunFixIn(ctx, "", argv)
}

// RunFixIn is RunFix with a working folder: `lore render` (tools.pointer_file)
// and `lore init` (repo.init) act on the repo they run in and lore has no
// -C flag, so the caller passes the check's target repo as dir ("" = this
// process's folder). A dir that is not a folder is refused before anything runs.
func (f setupFixRunner) RunFixIn(ctx context.Context, dir string, argv []string) (opssetup.FixRun, error) {
	if dir != "" {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return opssetup.FixRun{}, errs.New(errs.FixRefused, "fix folder "+strconv.Quote(probe.Sanitize(dir))+" is not a folder").
				WithHint("run lore setup again from inside the repo")
		}
	}
	if len(argv) == 0 || !slices.Contains(FixPrograms(), argv[0]) {
		got := ""
		if len(argv) > 0 {
			got = probe.Sanitize(argv[0])
		}
		return opssetup.FixRun{}, errs.New(errs.FixRefused, "lore runs only "+strings.Join(FixPrograms(), ", ")+" as a fix, not "+strconv.Quote(got)).
			WithHint("run the command yourself if you trust it")
	}
	bin := argv[0]
	if bin == FixProgramLore && f.lore != nil {
		if self, err := f.lore(); err == nil && self != "" {
			bin = self
		}
	}
	cctx, cancel := context.WithTimeout(ctx, FixTimeout)
	defer cancel()
	run := opssetup.FixRun{Argv: append([]string(nil), argv...)}
	start := time.Now()
	// This process's environment minus the repository-locating variables
	// (logic/gitenv): a fix runs git or gh in dir, and a GIT_DIR inherited
	// from a hook would send it to another repository.
	env := gitenv.Clean(os.Environ())
	if account := opssetup.FixGHAccountOf(ctx); bin == FixProgramGH && account != "" {
		// A gh fix acts as the setup's account, not gh's active login.
		kv, err := ghTokenEnv(cctx, account)
		switch {
		case errors.Is(err, exec.ErrNotFound):
			return run, errs.Wrap(errs.Unsupported, "gh is not installed on this machine", err).WithHint("install gh, then run the fix again")
		case err != nil:
			run.Exit, run.OutputTail = 1, err.Error()
			traceFix(ctx, argv, dir, run, nil, false, run.OutputTail, start)
			return run, nil
		}
		env = append(env, envGHPrompt, envGHNoCheck, kv)
	}
	cmd := exec.CommandContext(cctx, bin, argv[1:]...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = time.Second
	cmd.Dir = dir
	cmd.Env = env
	err := cmd.Run()
	defer func() { traceFix(ctx, argv, dir, run, err, cctx.Err() != nil, out.String(), start) }()
	run.OutputTail = fixTail(out.String())
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case cctx.Err() != nil:
		run.Exit = -1
		run.OutputTail = strings.TrimSpace(run.OutputTail + "\n" + "stopped after " + FixTimeout.String())
	case errors.As(err, &exitErr):
		run.Exit = exitErr.ExitCode()
	case errors.Is(err, exec.ErrNotFound):
		return run, errs.Wrap(errs.Unsupported, argv[0]+" is not installed on this machine", err).WithHint("install " + argv[0] + ", then run the fix again")
	default:
		return run, errs.Wrap(errs.Internal, "run fix "+argv[0], err)
	}
	return run, nil
}

// fixCallLabel prefixes a fix's trace label when ctx names no check.
const fixCallLabel = "fix"

// traceFix records one fix run in the request's probe trace (a no-op
// without one): the page's Details of the check row and its debug log
// then show a "Run it" fix like the check's own probes (ONB 5.3, 6.1).
// The label is the check id the op tagged (opssetup.WithFixCheck), so the
// check row's Details pick it up; probe.Record redacts every field. The
// argv is shown as the person typed it ("lore …", not the resolved path).
func traceFix(ctx context.Context, argv []string, dir string, run opssetup.FixRun, err error, timedOut bool, output string, start time.Time) {
	label := fixCallLabel
	if id := opssetup.FixCheckOf(ctx); id != "" {
		label = string(id)
	}
	words := make([]string, 0, len(argv))
	for _, a := range argv {
		words = append(words, shellWord(a))
	}
	c := probe.Call{Kind: probe.CallExec, Label: label, Command: strings.Join(words, " "), Dir: dir,
		Exit: run.Exit, TookMS: time.Since(start).Milliseconds(), TimedOut: timedOut, Output: output, Start: start}
	switch {
	case errors.Is(err, exec.ErrNotFound):
		c.Missing, c.Error = true, argv[0]+" is not on PATH"
	case timedOut:
		c.Error = "stopped after " + FixTimeout.String()
	}
	probe.Record(ctx, c)
}

// shellWord single-quotes w unless it is made only of characters a shell
// leaves alone, so the traced command pastes into a terminal as it ran.
func shellWord(w string) string {
	if w != "" && strings.IndexFunc(w, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@,+%~", r))
	}) < 0 {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}

// fixTail is the last FixTailLines non-empty lines of out, each passed
// through probe.Sanitize (no url userinfo, no token shapes).
func fixTail(out string) string {
	var lines []string
	for l := range strings.SplitSeq(out, "\n") {
		if s := probe.Sanitize(l); s != "" {
			lines = append(lines, s)
		}
	}
	if len(lines) > FixTailLines {
		lines = lines[len(lines)-FixTailLines:]
	}
	return strings.Join(lines, "\n")
}

// gh environment of a team gh run: never prompt, never print an update
// notice. GH_TOKEN carries the chosen account's token to the child only.
const (
	ghBin        = FixProgramGH
	envGHToken   = "GH_TOKEN"
	envGHPrompt  = "GH_PROMPT_DISABLED=1"
	envGHNoCheck = "GH_NO_UPDATE_NOTIFIER=1"
)

// reGHLogin is a GitHub login name (letters, digits, single dashes inside):
// what ghTokenEnv passes to gh auth token --user, so a value can never be
// read as a flag.
var reGHLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// ghTokenEnv is "GH_TOKEN=<token>" for gh login account, read with `gh
// auth token --user` (no prompt, no network). The token goes only into a
// gh child's environment: never into an argument, a log, a trace or a
// result (5.3, the secrets rule). The error is exec.ErrNotFound when gh is
// missing, else a fixed sentence the page can show (never gh's output).
func ghTokenEnv(ctx context.Context, account string) (string, error) {
	if !reGHLogin.MatchString(account) {
		return "", errors.New(strconv.Quote(probe.Sanitize(account)) + " is not a GitHub login; pick the account again on the GitHub step")
	}
	tok := exec.CommandContext(ctx, ghBin, "auth", "token", "--hostname", probe.GitHubHost, "--user", account)
	tok.Env = append(os.Environ(), envGHPrompt, envGHNoCheck)
	b, err := tok.Output()
	if errors.Is(err, exec.ErrNotFound) {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if err != nil || v == "" || strings.ContainsAny(v, " \t\r\n") {
		return "", errors.New("gh has no login for " + probe.Sanitize(account) + "; run gh auth login --hostname github.com, then try again")
	}
	return envGHToken + "=" + v, nil
}

// ghRunner is team.GitHubRunner: `gh <args>` as account. For a named
// account the token comes from `gh auth token --user <account>` and goes
// to the child's environment only: never into an argument, a log or the
// result (5.3, the secrets rule). Output is the sanitised tail.
type ghRunner struct{}

var _ team.GitHubRunner = ghRunner{}

// RunGH implements team.GitHubRunner.
func (ghRunner) RunGH(ctx context.Context, account string, args []string) (team.GHResult, error) {
	cctx, cancel := context.WithTimeout(ctx, FixTimeout)
	defer cancel()
	env := append(os.Environ(), envGHPrompt, envGHNoCheck)
	if account != "" {
		kv, err := ghTokenEnv(cctx, account)
		if errors.Is(err, exec.ErrNotFound) {
			return team.GHResult{}, errs.Wrap(errs.Unsupported, "gh is not installed", err).WithHint("install gh, then gh auth login")
		}
		if err != nil {
			return team.GHResult{Exit: 1, Output: err.Error()}, nil
		}
		env = append(env, kv)
	}
	cmd := exec.CommandContext(cctx, ghBin, args...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	res := team.GHResult{Output: fixTail(out.String())}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.Exit = exitErr.ExitCode()
	case errors.Is(err, exec.ErrNotFound):
		return res, errs.Wrap(errs.Unsupported, "gh is not installed", err).WithHint("install gh, then gh auth login")
	default:
		return res, errs.Wrap(errs.Internal, "run gh", err)
	}
	return res, nil
}
