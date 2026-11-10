//go:build journeys && !windows

package journeys

// invariants.go — the checks that run after EVERY step of EVERY journey, on
// every awake machine. Add one here (or in its own invariant_<name>.go with
// an init() calling RegisterInvariant) and every journey gets it at once.
// An invariant must hold for any person at any point of any journey; what is
// true only for one journey is a Fact (step.go).

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Ctx is what an invariant or fact gets: the test, the World, the machine
// checked (nil for a fact) and the step name to put in every message.
type Ctx struct {
	T    testing.TB
	W    *World
	M    *Machine
	Step string
}

// Errorf reports a broken invariant with the step in front.
func (c Ctx) Errorf(format string, args ...any) {
	c.T.Helper()
	c.T.Errorf("[%s] "+format, append([]any{c.Step}, args...)...)
}

// Invariant is one shared check.
type Invariant struct {
	Name  string
	Check func(c Ctx)
}

var invariants []Invariant

// RegisterInvariant adds a shared invariant (call it from init()). Names are
// unique; a second registration of a name is a harness bug.
func RegisterInvariant(inv Invariant) {
	for _, have := range invariants {
		if have.Name == inv.Name {
			panic("journeys: invariant " + inv.Name + " registered twice")
		}
	}
	invariants = append(invariants, inv)
}

func init() {
	RegisterInvariant(Invariant{Name: "readable-output", Check: checkReadable})
	RegisterInvariant(Invariant{Name: "doctor", Check: checkDoctor})
	RegisterInvariant(Invariant{Name: "config", Check: checkConfig})
	RegisterInvariant(Invariant{Name: "modes", Check: checkModes})
}

// killed is the exit of a lore run the harness killed on purpose
// (SleepMidPush): its output is cut off by design.
const killed = 137

// unreadable is text that never belongs in what a person reads: a crash, a
// Go formatting error, a nil printed as a value.
var unreadable = []string{"panic:", "goroutine ", "%!", "<nil>", "runtime error"}

// checkReadable: every answer lore printed in this step on this machine is
// free of crash and formatting debris, and a failure says something.
func checkReadable(c Ctx) {
	c.T.Helper()
	for _, r := range c.M.results {
		if r.Code == killed || r.Code < 0 {
			continue
		}
		out := r.Stdout + r.Stderr
		for _, bad := range unreadable {
			if strings.Contains(out, bad) {
				c.Errorf("lore %s printed %q:\n%s", strings.Join(r.Args, " "), bad, out)
			}
		}
		if r.Code != 0 && strings.TrimSpace(out) == "" {
			c.Errorf("lore %s exited %d and said nothing", strings.Join(r.Args, " "), r.Code)
		}
	}
}

// doctorReport is the part of `lore doctor --json` a person reads.
type doctorReport struct {
	Status     string        `json:"status"`
	ExitStatus int           `json:"exit_status"`
	Machine    doctorSection `json:"machine"`
	Checks     []doctorCheck `json:"checks"`
	Warnings   []string      `json:"warnings"`
	// Setups holds a repo report's per-setup checks (tools, stores).
	Setups []doctorSection `json:"setups"`
	Counts struct {
		Warn  int `json:"warn"`
		Fail  int `json:"fail"`
		Error int `json:"error"`
	} `json:"counts"`
}

type doctorSection struct {
	Checks []doctorCheck `json:"checks"`
}

type doctorCheck struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Target string `json:"target"`
	Detail string `json:"detail"`
	// Fix is a string in the machine section and an object
	// {text, command, runnable, outward} in a setup's checks.
	Fix json.RawMessage `json:"fix"`
}

// fixText is what the fix tells the person, whichever shape it has.
func (c doctorCheck) fixText() string {
	if len(c.Fix) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(c.Fix, &s) == nil {
		return s
	}
	var o struct {
		Text    string `json:"text"`
		Command string `json:"command"`
	}
	if json.Unmarshal(c.Fix, &o) == nil {
		return strings.TrimSpace(o.Text + " " + o.Command)
	}
	return string(c.Fix)
}

// doctorStatus maps doctor's documented exits to its statuses.
var doctorStatus = map[int]string{0: "healthy", 1: "degraded", 2: "broken"}

// checkDoctor: doctor answers in HOME and in every clone with a report whose
// status, exit and counts agree, and every check that is not ok says what it
// checked and what is wrong or what to do.
func checkDoctor(c Ctx) {
	c.T.Helper()
	dirs := []string{c.M.Home}
	for _, cl := range c.M.Clones() {
		dirs = append(dirs, cl.Dir)
	}
	for _, dir := range dirs {
		args := []string{"doctor", "--json"}
		if dir == c.M.Home {
			args = []string{"doctor", "--all", "--json"}
		}
		r := c.M.Actor.Lore(dir, args...) // not recorded: the person's own runs are
		where := rel(c.M.Home, dir)
		want, documented := doctorStatus[r.Code]
		if !documented || strings.Contains(r.Stderr, "panic") {
			c.Errorf("doctor in %s: %s", where, r)
			continue
		}
		var d doctorReport
		if err := json.Unmarshal([]byte(r.Stdout), &d); err != nil || d.Status == "" {
			c.Errorf("doctor in %s printed no report (%v): %s", where, err, r)
			continue
		}
		if d.Status != want || d.ExitStatus != r.Code {
			c.Errorf("doctor in %s contradicts itself: status %q, exit_status %d, process exit %d", where, d.Status, d.ExitStatus, r.Code)
		}
		bad := 0
		all := append(slices.Clone(d.Machine.Checks), d.Checks...)
		for _, su := range d.Setups {
			all = append(all, su.Checks...)
		}
		for _, ch := range all {
			if ch.Status == "ok" || ch.Status == "skip" {
				continue
			}
			bad++
			if ch.Check == "" || (strings.TrimSpace(ch.Detail) == "" && ch.fixText() == "") {
				c.Errorf("doctor in %s: check %s (%s) is %s but does not say what or why", where, ch.Check, ch.Target, ch.Status)
			}
		}
		if d.Status == "healthy" && d.Counts.Fail+d.Counts.Error > 0 {
			c.Errorf("doctor in %s says healthy with %d failing checks", where, d.Counts.Fail+d.Counts.Error)
		}
		for _, wn := range d.Warnings {
			if strings.TrimSpace(wn) == "" {
				c.Errorf("doctor in %s has an empty warning", where)
			}
		}
		if d.Status != "healthy" && bad == 0 && len(d.Warnings) == 0 && d.Counts.Warn+d.Counts.Fail+d.Counts.Error == 0 {
			c.Errorf("doctor in %s says %s but names no check:\n%s", where, d.Status, r.Stdout)
		}
	}
}

// checkConfig: once lore wrote ~/.lore/config.yaml, config check knows every
// key in it.
func checkConfig(c Ctx) {
	c.T.Helper()
	if _, err := os.Stat(c.M.LorePath("config.yaml")); err != nil {
		return
	}
	r := c.M.Actor.Lore(c.M.Home, "config", "check", "--json")
	if r.Code != 0 {
		c.Errorf("config check: %s", r)
		return
	}
	var out struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		c.Errorf("config check printed no JSON (%v): %s", err, r)
		return
	}
	for _, w := range out.Warnings {
		c.Errorf("config check warns: %s", w)
	}
}

// Mode rules: ~/.lore is this machine's and owner-only; what lore writes
// under a repo's .lore/ is committed and read by every clone.
const (
	privateBits fs.FileMode = 0o077 // no group or other access at all
	repoRead    fs.FileMode = 0o044 // group and others read
)

// checkModes: nothing under ~/.lore is readable by another user (a file
// counts as private when it or a folder above it, up to ~/.lore, shuts out
// group and others: the tests/machines rule); every file under a clone's
// .lore/ that git would commit is readable by group and others.
func checkModes(c Ctx) {
	c.T.Helper()
	root := c.M.LorePath()
	if info, err := os.Lstat(root); err == nil && !info.IsDir() {
		return // the person's own file named .lore: its mode is theirs
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !(d.IsDir() || d.Type().IsRegular()) {
			return nil
		}
		if info, e := d.Info(); e == nil && !shutOut(root, p) {
			c.Errorf("%s is %v; ~/.lore must be owner-only", rel(c.M.Home, p), info.Mode().Perm())
		}
		return nil
	})
	for _, cl := range c.M.Clones() {
		_ = filepath.WalkDir(filepath.Join(cl.Dir, ".lore"), func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			if info, e := d.Info(); e == nil && info.Mode().Perm()&repoRead != repoRead && !ignoredInRepo(c.W, cl, p) {
				c.Errorf("%s is %v; files under a repo's .lore/ are committed and must be readable (0644)", p, info.Mode().Perm())
			}
			return nil
		})
	}
}

// shutOut: p or a folder between it and root has no group/other bits.
func shutOut(root, p string) bool {
	for q := p; ; q = filepath.Dir(q) {
		if info, err := os.Stat(q); err == nil && info.Mode().Perm()&privateBits == 0 {
			return true
		}
		if q == root || q == filepath.Dir(q) {
			return false
		}
	}
}

// ignoredInRepo reports whether git ignores p in the clone (not committed,
// so the repo-mode rule does not apply).
func ignoredInRepo(w *World, cl *teamharness.Clone, p string) bool {
	return w.TryGit(cl.Dir, "check-ignore", "-q", p).Code == 0
}

// rel shows p relative to home for messages.
func rel(home, p string) string {
	if r, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(r, "..") {
		return "~/" + r
	}
	return p
}
