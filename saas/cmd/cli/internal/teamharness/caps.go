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
