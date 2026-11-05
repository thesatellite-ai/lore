// Package check defines `lore check leaks` and `lore check pr-body`
// (layer L5, LORE_TEAM_SPEC F16 and 6.3): the copy-paste catch between
// internal or personal content and anything a client can read. The
// pre-commit and commit-msg hooks run `lore check leaks --staged` and
// `--commit-msg <file>`; agents run `lore check pr-body` before opening a
// pull request or posting a comment; the client's CI runs
// `lore check leaks --ci`, which needs no store and names nothing
// internal.
//
//	check leaks [--staged] [--commit-msg f]  Checker.CheckLeaks (staged files +
//	                                         message vs the internal rows, the
//	                                         secret scan, metadata rules FB-META-1)
//	check leaks --ci                         ids and structure of .lore/data only
//	check leaks --as-client                  the whole repo as the client sees it
//	check pr-body <file|->                   read the text ─► Checker.CheckText
//
// Every refusal is E_LEAK_BLOCKED (exit class guard); a check that cannot
// read what it needs fails closed. The rules live behind Checker (the
// composition root's leak index, org resolution and scanner); this
// package owns the command surface, its inputs and its outputs. These
// operations read files and stores but write no row, so they are Local
// (P3-CFG-1): a hook never waits on an engine open.
//
// What this cannot stop is listed in LORE_TEAM_SPEC 6.6: --no-verify, the
// web UI, paraphrase, a teammate without lore.
package check

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore check …`).
const (
	OpLeaks  = "check.leaks"
	OpPRBody = "check.pr_body"
)

// Where a text finding is reported (the v0.1.17 labels).
const (
	// WherePRBody labels a pull request body or comment.
	WherePRBody = "PR body"
	// stdinArg reads the body from stdin.
	stdinArg = "-"
	// maxBodyBytes bounds a PR body read (a body is text, never a file dump).
	maxBodyBytes = 8 << 20
)

// Leak check modes (LeaksOut.Mode).
const (
	ModeCommit   = "commit"
	ModeCI       = "ci"
	ModeAsClient = "as_client"
)

// prBodyClean is the line a clean pr-body check prints (v0.1.17).
const prBodyClean = "✓ nothing internal found (a copy-paste check: a paraphrase passes it)"

// LeaksIn is the input of check.leaks.
type LeaksIn struct {
	CI        bool   `json:"ci,omitempty" flag:"ci" desc:"CI mode: ids and structure only, no store access, names nothing internal"`
	Explain   bool   `json:"explain,omitempty" flag:"explain" desc:"print the rule, the source and the matched words of each finding"`
	Staged    bool   `json:"staged,omitempty" flag:"staged" desc:"check the staged files (default unless --commit-msg is given alone)"`
	CommitMsg string `json:"commit_msg,omitempty" flag:"commit-msg" desc:"also check this commit message file (the commit-msg hook's $1)"`
	AsClient  bool   `json:"as_client,omitempty" flag:"as-client" desc:"check the whole repo as the client would see it (before a hand-over)"`
}

// Mode is the check the flags ask for.
func (in LeaksIn) Mode() string {
	switch {
	case in.CI:
		return ModeCI
	case in.AsClient:
		return ModeAsClient
	}
	return ModeCommit
}

// LeaksOut is a passed check: the mode and the line it printed (empty for
// a hook's commit check, which is silent when nothing is found).
type LeaksOut struct {
	Mode    string `json:"mode"`
	Message string `json:"message,omitempty"`
}

// Text prints the message line, nothing for a silent pass.
func (o LeaksOut) Text() string {
	if o.Message == "" {
		return ""
	}
	return o.Message + "\n"
}

// PRBodyIn is the input of check.pr_body.
type PRBodyIn struct {
	File    string `json:"file" arg:"0" desc:"the body file, or - for stdin"`
	Explain bool   `json:"explain,omitempty" flag:"explain" desc:"print the rule, the source and the matched words of each finding"`
}

// TextIn is one text Checker.CheckText checks (a PR body or comment).
type TextIn struct {
	Where   string
	Body    []byte
	Explain bool
}

// PRBodyOut is a passed pr-body check.
type PRBodyOut struct {
	Clean   bool   `json:"clean"`
	Message string `json:"message"`
}

// Text is the v0.1.17 confirmation line.
func (o PRBodyOut) Text() string { return o.Message + "\n" }

// Checker runs the leak rules; *wire.App implements it (the binary's
// leak index, org resolution and scanner).
type Checker interface {
	// CheckLeaks runs check.leaks in the mode in asks for; a finding is
	// E_LEAK_BLOCKED with the block report as its message.
	CheckLeaks(ctx context.Context, in LeaksIn) (LeaksOut, error)
	// CheckText checks one text the person is about to post.
	CheckText(ctx context.Context, in TextIn) error
}

// leakErrors are the codes the check operations return.
var leakErrors = []errs.Code{errs.InvalidInput, errs.LeakBlocked, errs.ConfigInvalid, errs.LinkUnknown,
	errs.StoreUnreachable, errs.SecretDetected, errs.Unsupported, errs.Internal}

// Ops returns the check operations bound to c.
func Ops(c Checker) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name:    OpLeaks,
			Summary: "Check staged files and the commit message (hooks), or the tree structure (--ci), for internal or personal content",
			// Read-only: a check writes no row. Local: a hook never opens the engine (P3-CFG-1).
			Permission: op.PermRead, Access: op.AccessRead, Errors: leakErrors, Local: true,
			Examples: []op.Example{
				{Title: "staged files, as the pre-commit hook does", CLI: "lore check leaks", Want: ""},
				{Title: "the commit message", CLI: "lore check leaks --commit-msg .git/COMMIT_EDITMSG", Want: ""},
				{Title: "in the client's CI", CLI: "lore check leaks --ci", Want: "✓"},
				{Title: "before handing an own repo to the client", CLI: "lore check leaks --as-client", Want: "✓"},
			},
		}, func(ctx context.Context, in LeaksIn) (LeaksOut, error) {
			if in.CI && in.AsClient {
				return LeaksOut{}, errs.New(errs.InvalidInput, "check leaks: --ci and --as-client cannot be used together").
					WithHint("--ci is the client's CI check; --as-client is the hand-over check of an own repo")
			}
			out, err := c.CheckLeaks(ctx, in)
			if err != nil {
				return LeaksOut{}, err
			}
			out.Mode = in.Mode()
			return out, nil
		}),
		op.Define(op.Spec{
			Name:       OpPRBody,
			Summary:    "Check a pull request body or comment before posting it (agents: always)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: leakErrors, Local: true,
			Examples: []op.Example{
				{Title: "a body file", CLI: "lore check pr-body body.md", Want: "✓"},
				{Title: "from stdin", CLI: "gh pr view 12 --json body -q .body | lore check pr-body -", Want: "✓"},
			},
		}, func(ctx context.Context, in PRBodyIn) (PRBodyOut, error) {
			body, err := readBody(in.File)
			if err != nil {
				return PRBodyOut{}, err
			}
			if err := c.CheckText(ctx, TextIn{Where: WherePRBody, Body: body, Explain: in.Explain}); err != nil {
				return PRBodyOut{}, err
			}
			return PRBodyOut{Clean: true, Message: prBodyClean}, nil
		}),
	}
}

// readBody reads the body file, or stdin for "-", bounded by maxBodyBytes.
func readBody(file string) ([]byte, error) {
	var r io.Reader = os.Stdin
	if file != stdinArg {
		fh, err := os.Open(filepath.Clean(file))
		if err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "read the body", err)
		}
		defer func() { _ = fh.Close() }() // read only
		r = fh
	}
	b, err := io.ReadAll(io.LimitReader(r, maxBodyBytes+1))
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "read the body", err)
	}
	if len(b) > maxBodyBytes {
		return nil, errs.New(errs.InvalidInput, "check pr-body: the body is over "+strconv.Itoa(maxBodyBytes)+" bytes")
	}
	return b, nil
}
