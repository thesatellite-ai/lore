package teamharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PendingPrefix starts the skip message of a test whose feature is not built
// yet. The matrix tool (teamharness/matrix) counts these as "pending", not
// as passes; LORE_TEAM_STRICT=1 turns them into failures.
const PendingPrefix = "PENDING:"

// Cap is one piece of CLI surface a test needs: a command path, optionally
// with a flag on it. The probe is `lore <path> --help`, read the way a user
// would read it, so a capability counts as present only once the binary
// really offers it. A present command that misbehaves then fails the test
// (red), which is the point: pending means "not built", never "broken".
type Cap struct {
	Path []string // e.g. {"task", "claim"}
	Flag string   // e.g. "live" for --live; "" = the command alone
}

// Cmd is the capability "lore <path...> exists".
func Cmd(path ...string) Cap { return Cap{Path: path} }

// Flag is the capability "lore <path...> takes --<flag>".
func Flag(flag string, path ...string) Cap { return Cap{Path: path, Flag: flag} }

func (c Cap) String() string {
	s := "lore " + strings.Join(c.Path, " ")
	if c.Flag != "" {
		s += " --" + c.Flag
	}
	return s
}

// Require marks the test pending (skip with PendingPrefix) unless every
// capability is present in the binary. Call it first in a test, listing
// every command and flag the scenario uses, including fixture setup.
func (w *World) Require(caps ...Cap) {
	w.T.Helper()
	if os.Getenv(EnvForce) == "1" {
		return
	}
	var missing []string
	for _, c := range caps {
		if !w.has(c) {
			missing = append(missing, c.String())
		}
	}
	if len(missing) == 0 {
		return
	}
	pending(w.T, "not built yet: "+strings.Join(missing, ", "))
}

// pending ends the test as PENDING (a skip carrying PendingPrefix, which
// the matrix reports as missing work), or fails it with LORE_TEAM_STRICT=1.
func pending(t testing.TB, reason string) {
	t.Helper()
	msg := PendingPrefix + " " + reason
	if os.Getenv(EnvStrict) == "1" {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

// Has reports whether a capability is present (for tests that branch, e.g.
// to probe an optional doctor row).
func (w *World) Has(c Cap) bool { return w.has(c) }

func (w *World) has(c Cap) bool {
	w.T.Helper()
	home := filepath.Join(w.Root, "probe-home")
	mustMkdir(w.T, home)
	r := w.run(w.Root, w.baseEnv(home, "probe"), w.Bin, append(append([]string{}, c.Path...), "--help")...)
	if r.Code != 0 {
		return false
	}
	usage := "Usage:\n  lore " + strings.Join(c.Path, " ")
	i := strings.Index(r.Stdout, usage)
	if i < 0 {
		return false
	}
	// "lore task" must not match the usage line of "lore task claim"'s parent:
	// the path must end at a space, a newline or the end of the text.
	rest := r.Stdout[i+len(usage):]
	if rest != "" && rest[0] != ' ' && rest[0] != '\n' {
		return false
	}
	if c.Flag == "" {
		return true
	}
	flag := "--" + c.Flag
	for _, line := range strings.Split(r.Stdout, "\n") {
		for _, f := range strings.Fields(line) {
			if strings.TrimSuffix(f, ",") == flag || strings.HasPrefix(f, flag+"=") {
				return true
			}
		}
	}
	return false
}

// Bug is a known open defect that a regression test pins before its fix
// lands. While Open, the test goes PENDING — not red — at the point where
// today's binary shows the defect, so the matrix keeps reporting it as
// missing work owned by Owner. The gate fires only on the defect itself:
// any other failure on the way is still red, and once the fix lands the
// test passes on its own. Flip Open to false when the fix is merged, so a
// later regression fails instead of hiding as pending.
type Bug struct {
	ID    string // e.g. "BUG-D1"
	Owner string // the session that fixes it, e.g. "lore-stores"
	Open  bool
}

// PendingIf marks the test PENDING when the bug is open and seen is true
// (the defect showed); LORE_TEAM_STRICT=1 turns that into a failure.
func (b Bug) PendingIf(t testing.TB, seen bool, what string) {
	t.Helper()
	if b.Open && seen {
		pending(t, b.ID+" open ("+b.Owner+"): "+what)
	}
}

// PendingOn is PendingIf for a command whose output holds every symptom
// string (and no symptom given never matches).
func (b Bug) PendingOn(t testing.TB, r Result, symptom ...string) {
	t.Helper()
	seen := len(symptom) > 0
	for _, s := range symptom {
		seen = seen && strings.Contains(r.Out(), s)
	}
	b.PendingIf(t, seen, strings.Join(symptom, " … "))
}
