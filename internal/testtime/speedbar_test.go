package testtime

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rawWallBar is a test comparing a measured wall time with a literal
// duration or a named budget: `d > 5*time.Second`, `took > jMergeBudget`,
// `time.Since(start) > 2*time.Second`.
var rawWallBar = regexp.MustCompile(`(\btook|\bd|\belapsed|time\.Since\([^)]*\))\s*>\s*([0-9.]+\s*\*\s*time\.(Millisecond|Second|Minute)|time\.(Second|Minute)\b|[A-Za-z]\w*Budget\b)`)

// behaviourBound is one raw wall-time bar that is NOT a speed bar: it
// bounds a timeout, a kill or a lock wait that is itself the behaviour
// under test, or checks that a stamped time is "now", so it stays as
// written on every machine (D-417).
type behaviourBound struct {
	file string // slash path from the module root
	code string // a piece of the line
	why  string
}

var behaviourBounds = []behaviourBound{
	{"wire/fixall_stop_test.go", "d > 5*time.Second", "the fix-all child is stopped at its 700 ms limit"},
	{"wire/doctor_more_test.go", "took > 3*time.Second", "no wait on another repo's held cache lock"},
	{"wire/setupaliases_leak_internal_test.go", "took > 2*time.Second", "Close ends a slow alias instead of waiting it out"},
	{"wire/migrate_lock_test.go", "d > 2*time.Second", "a dead holder's migrate lock is taken over at once"},
	{"wire/migrate_lock_test.go", "d > time.Second", "a live holder's migrate lock refuses at once"},
	{"tests/journeys/solo_test.go", "d > 20*time.Second", "no wait on the lock a killed push left"},
	{"tests/journeys/land7_a_test.go", "d > 25*time.Second", "the end hook is bounded by its 15 s push limit"},
	{"app/sessionend_test.go", "d > 30*time.Second", "the session-end hook is bounded by its push limit"},
	{"adapters/probe/git_test.go", "time.Since(start) > 5*time.Second", "the probe kills git at its 300 ms deadline"},
	{"adapters/backend/folder/folder_test.go", "time.Since(start) > 2*time.Second", "the store lock timeout is honoured"},
	{"adapters/secretmgr/manager_test.go", "time.Since(start) > 30*time.Second", "a hanging manager CLI is killed at its timeout"},
	{"adapters/live/live_cov_test.go", "time.Since(got) > time.Minute", "a signature is stamped with the current time (a clock check)"},
	{"wire/settings_refresh_test.go", "d > time.Minute", "a refresh is stamped with the current time (a clock check)"},
	{"tests/teame2e/teamharness/deadline_test.go", "time.Since(start) > 2*time.Second+cmdKillGrace", "runWithDeadline's own kill deadline"},
}

// isBehaviourBound reports whether code (a line of file rel) is listed.
func isBehaviourBound(rel, code string) bool {
	for _, b := range behaviourBounds {
		if b.file == rel && strings.Contains(code, b.code) {
			return true
		}
	}
	return false
}

// rawBars lists "file:line: code" for every raw wall-time bar in the
// module's test files that goes through neither this package nor
// behaviourBounds.
func rawBars(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/testtime/") {
			return nil // this guard's own samples
		}
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "//") || !rawWallBar.MatchString(code) || strings.Contains(code, "testtime.") || isBehaviourBound(rel, code) {
				continue
			}
			out = append(out, rel+":"+strconv.Itoa(i+1)+": "+code)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestSpeedBarsScale (devco:D-417, owner option A): every product speed
// bar in a test ("finishes within N") goes through SpeedBar (or Scale), so
// it is exactly as written at factor 1 and stretches on a slower machine; a
// bar that bounds a timeout, a kill or a lock wait (the behaviour itself)
// is listed in behaviourBounds with its reason and stays raw. A new raw
// wall-time bar fails here until it is one or the other.
func TestSpeedBarsScale(t *testing.T) {
	root := repoRoot(t)
	for _, bar := range rawBars(t, root) {
		t.Errorf("%s\n  a raw wall-time bar: a speed bar goes through testtime.SpeedBar (as written at factor 1); a timeout, kill or lock bound under test goes in behaviourBounds with its reason", bar)
	}
	for _, bb := range behaviourBounds {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(bb.file)))
		if err != nil || !strings.Contains(string(b), bb.code) {
			t.Errorf("behaviourBounds names %s %q, which is not there any more: drop or update the entry", bb.file, bb.code)
		}
	}
}

// TestSpeedBarGuardSeesRawBars: the pattern matches the raw shapes (so a
// pattern typo cannot make the guard pass everything) and not other
// comparisons; SpeedBar keeps a bar exactly as written at factor 1.
func TestSpeedBarGuardSeesRawBars(t *testing.T) {
	for _, raw := range []string{
		`if d := time.Since(start); d > 5*time.Second {`, `if took > jMergeBudget {`, `if took > 15*time.Second {`,
		`if time.Since(start) > 2*time.Second {`, `if took := time.Since(start); took > land7bDoctorBudget {`,
		`if d := time.Since(start); d > time.Second {`, `if took > 1500*time.Millisecond {`,
	} {
		if !rawWallBar.MatchString(raw) {
			t.Errorf("not seen as a raw bar: %s", raw)
		}
	}
	for _, ok := range []string{`if len(rows) > 3000 {`, `if n > gManyAgentsBusy {`, `if p95.took > gManyAgentsFairness*p50.took {`} {
		if rawWallBar.MatchString(ok) {
			t.Errorf("seen as a raw bar: %s", ok)
		}
	}
	for _, d := range []time.Duration{1500 * time.Millisecond, 30 * time.Second} {
		if got := ScaleBy(d, 1); got != d {
			t.Errorf("factor 1 changed the bar %v to %v", d, got)
		}
	}
}
