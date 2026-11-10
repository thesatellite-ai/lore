package teamharness

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Exit codes the spec fixes for coordination errors (LORE_TEAM_SPEC.md §1
// "Error codes"). Tests assert on these numbers because scripts and agents
// branch on them; the E_* string must also appear in the output.
const (
	ExitOK          = 0
	ExitError       = 1  // unexpected failures only: E_INTERNAL or no lore code (Q-R2-5)
	ExitNoWork      = 10 // E_NO_WORK
	ExitContended   = 11 // E_CONTENDED
	ExitLostClaim   = 12 // E_LOST_CLAIM
	ExitCancelled   = 13 // E_CANCELLED
	ExitReassigned  = 14 // E_REASSIGNED
	ExitOffline     = 15 // E_OFFLINE
	ExitAmbiguousID = 16 // E_AMBIGUOUS_ID
	ExitOrg         = 17 // E_ORG_AMBIGUOUS, E_ORG_DISAGREES
	ExitLinkUnknown = 18 // E_LINK_UNKNOWN
	ExitGuard       = 19 // E_NOT_A_STORE, E_LEAK_BLOCKED, E_CYCLE, E_OLD_LORE
	ExitNotFound    = 27 // E_NOT_FOUND (0.1.18, decision DOCS O4 / BATCH 7; v0.1.17 exited 1)
	// Q-R2-5: old commands exit their code's core class exit too, so a
	// refusal (E_UNSUPPORTED, E_REFUSED, …) is 23 and an unconfirmed
	// question with no terminal (E_NEEDS_CONFIRM) is 26, never ExitError.
	ExitRefused      = 23 // class refused (E_UNSUPPORTED, E_REFUSED, E_SECRET_DETECTED, …)
	ExitNeedsConfirm = 26 // E_NEEDS_CONFIRM
)

// Result is one finished process: what a user would see on the terminal.
type Result struct {
	Args   []string
	Stdout string
	Stderr string
	Code   int
	// Unbuilt names the command when lore printed its group's help instead of
	// running (cobra exits 0 for an unknown subcommand of an existing group,
	// e.g. `lore actor register` before it exists); empty otherwise. Every
	// assertion on such a Result marks the test PENDING, never a pass.
	Unbuilt string
}

// pendingIfUnbuilt stops the test as PENDING (or fails it with
// LORE_TEAM_STRICT=1) when the command was not in the binary.
func (r Result) pendingIfUnbuilt(t testing.TB) {
	t.Helper()
	if r.Unbuilt == "" {
		return
	}
	pending(t, "not built yet: "+r.Unbuilt+" (lore printed the group help)")
}

// helpArgs are the arguments that ask for help on purpose; a group help
// printed for them is the answer, not a missing command.
var helpArgs = map[string]bool{"--help": true, "-h": true, "help": true}

// unbuiltCommand reports the command lore did not run when its stdout is
// the help of a command group although no help was asked for: "lore actor
// register" when stdout is the usage of `lore actor [command]`.
func unbuiltCommand(args []string, stdout string) string {
	if !strings.Contains(stdout, "\nAvailable Commands:\n") {
		return ""
	}
	for _, a := range args {
		if helpArgs[a] {
			return ""
		}
	}
	var path []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			break
		}
		path = append(path, a)
		if strings.Contains(stdout, "Usage:\n  lore "+strings.Join(path, " ")+" [command]") {
			if len(path) < len(args) {
				return "lore " + strings.Join(append(path, args[len(path)]), " ")
			}
			return "lore " + strings.Join(path, " ")
		}
	}
	return ""
}

// Out is stdout and stderr together, the way a terminal shows them.
func (r Result) Out() string { return r.Stdout + r.Stderr }

// String describes the run for failure messages.
func (r Result) String() string {
	return fmt.Sprintf("lore %s → exit %d\nstdout: %s\nstderr: %s", strings.Join(r.Args, " "), r.Code, r.Stdout, r.Stderr)
}

// OK fails the test unless the command exited 0.
func (r Result) OK(t testing.TB) Result {
	t.Helper()
	r.pendingIfUnbuilt(t)
	if r.Code != 0 {
		t.Fatalf("expected success: %s", r)
	}
	return r
}

// Fails fails the test unless the command exited with code and printed
// every one of the given strings (typically an E_* code and a hint).
func (r Result) Fails(t testing.TB, code int, mustSay ...string) Result {
	t.Helper()
	r.pendingIfUnbuilt(t)
	if r.Code != code {
		t.Fatalf("expected exit %d: %s", code, r)
	}
	for _, s := range mustSay {
		if !strings.Contains(r.Out(), s) {
			t.Fatalf("expected output to contain %q: %s", s, r)
		}
	}
	return r
}

// Refused fails the test unless the command exited non-zero and said every
// one of the given strings. Use it where the spec fixes the refusal but not
// its exit code.
func (r Result) Refused(t testing.TB, mustSay ...string) Result {
	t.Helper()
	r.pendingIfUnbuilt(t)
	if r.Code == 0 {
		t.Fatalf("expected a refusal: %s", r)
	}
	for _, s := range mustSay {
		if !strings.Contains(r.Out(), s) {
			t.Fatalf("expected output to contain %q: %s", s, r)
		}
	}
	return r
}

// Says fails the test unless the output contains every string.
func (r Result) Says(t testing.TB, mustSay ...string) Result {
	t.Helper()
	r.pendingIfUnbuilt(t)
	for _, s := range mustSay {
		if !strings.Contains(r.Out(), s) {
			t.Fatalf("expected output to contain %q: %s", s, r)
		}
	}
	return r
}

// Never fails the test if the output contains any of the strings (used for
// leak checks on output: redaction, no internal names).
func (r Result) Never(t testing.TB, mustNotSay ...string) Result {
	t.Helper()
	r.pendingIfUnbuilt(t)
	for _, s := range mustNotSay {
		if s != "" && strings.Contains(r.Out(), s) {
			t.Fatalf("output must not contain %q: %s", s, r)
		}
	}
	return r
}

// DecodeJSON decodes a successful command's stdout into a T. Tests declare
// small structs holding only the fields the spec promises, so an added field
// never breaks them and a renamed one does.
func DecodeJSON[T any](t testing.TB, r Result) T {
	t.Helper()
	r.OK(t)
	var v T
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("decode JSON from %s: %v", r, err)
	}
	return v
}
