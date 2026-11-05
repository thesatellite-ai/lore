// program_cmd.go — the one way the old CLI runs an external program (git,
// xattr, which): a Cmd built from the absolute path exec.LookPath resolved,
// one argv entry per argument (no shell ever parses them), killed when its
// context ends. It is the root module's wire/gitexec.go shape (P2-LINT-4),
// so a variable argument is plainly data, never a command line.
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
)

// programCmd is one prepared run of an external program. Set Env, Stdin,
// Stdout or Stderr before calling Run, Output or CombinedOutput, exactly as
// on an exec.Cmd.
type programCmd struct {
	ctx     context.Context
	cmd     *exec.Cmd
	lookErr error
	// Env is the child's environment; nil inherits this process's.
	Env []string
	// Stdin, Stdout and Stderr are the child's streams; nil means none
	// (os.DevNull), as on exec.Cmd.
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// newProgramCmd prepares name (looked up on PATH) with args. A program that
// is not installed fails when the command runs, with exec's lookup error.
func newProgramCmd(ctx context.Context, name string, args ...string) *programCmd {
	bin, err := exec.LookPath(name)
	if err != nil {
		return &programCmd{ctx: ctx, lookErr: err}
	}
	return &programCmd{ctx: ctx, cmd: &exec.Cmd{Path: bin, Args: append([]string{bin}, args...)}}
}

// Run starts the program, waits for it, and kills it when ctx ends first;
// then it returns ctx's error.
func (p *programCmd) Run() error {
	if p.lookErr != nil {
		return p.lookErr
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	c := p.cmd
	c.Env, c.Stdin, c.Stdout, c.Stderr = p.Env, p.Stdin, p.Stdout, p.Stderr
	if err := c.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	killed := make(chan struct{})
	go func() {
		select {
		case <-p.ctx.Done():
			_ = c.Process.Kill() // the process may have exited already; Wait reports the outcome
			close(killed)
		case <-done:
		}
	}()
	werr := c.Wait()
	close(done)
	select {
	case <-killed:
		return p.ctx.Err()
	default:
	}
	return werr
}

// Output runs the program and returns its stdout. As exec.Cmd.Output does,
// a failed run with no Stderr set carries the program's stderr in the
// *exec.ExitError.
func (p *programCmd) Output() ([]byte, error) {
	var out, errOut bytes.Buffer
	p.Stdout = &out
	captureErr := p.Stderr == nil
	if captureErr {
		p.Stderr = &errOut
	}
	err := p.Run()
	var ee *exec.ExitError
	if captureErr && errors.As(err, &ee) {
		ee.Stderr = errOut.Bytes()
	}
	return out.Bytes(), err
}

// CombinedOutput runs the program and returns stdout and stderr together.
func (p *programCmd) CombinedOutput() ([]byte, error) {
	var b bytes.Buffer
	p.Stdout, p.Stderr = &b, &b
	err := p.Run()
	return b.Bytes(), err
}
