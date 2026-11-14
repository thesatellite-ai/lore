//go:build stories && !windows

package stories

// I_helpers_test.go — small helpers of story I (two machines, text,
// environment). Prefixed i… so the other stories' helper files in this
// package never clash with them.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// iRowID matches the first row id a command prints.
var iRowID = regexp.MustCompile(`(mem|dec|rul|tsk)_[0-9a-f]{32}`)

// iActorID matches an actor id.
var iActorID = regexp.MustCompile(`act_[0-9a-f]{32}`)

// iFinding runs a step that guards one finding of this story (once
// waiting on it, now fixed): a subtest named after the finding id, so a
// failure names the finding it brings back and ends only this step, never
// the story around it. The regression index names the story test as that
// finding's guard.
func iFinding(t *testing.T, finding string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(finding, func(t *testing.T) {
		fn(t)
	})
}

// iAdded is the id an add printed; a missing id fails the step.
func iAdded(t testing.TB, r teamharness.Result) string {
	t.Helper()
	id := iRowID.FindString(r.Out())
	if id == "" {
		t.Fatalf("the add printed no row id: %s", r)
	}
	return id
}

// iShown is what `lore show <id> --json` says a row holds.
type iShown struct {
	ID     string                     `json:"id"`
	Fields map[string]json.RawMessage `json:"fields"`
}

// Str is a text field's value ("" when absent or not text).
func (s iShown) Str(field string) string {
	var v string
	_ = json.Unmarshal(s.Fields[field], &v)
	return v
}

// iShow reads a row back through `lore show --json`: the bytes a reader's
// tools get, so a round trip is checked on them, not on the terminal view.
func iShow(t testing.TB, m *journeys.Machine, c *teamharness.Clone, id string) iShown {
	t.Helper()
	r := m.LoreIn(c, "show", id, "--json").OK(t)
	var out iShown
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("show %s --json is not JSON: %v\n%s", id, err, r)
	}
	return out
}

// iSettle closes every open sync conflict of one laptop, keeping its own
// side, and syncs: a step that measures what one sync moves (D-121) needs a
// clean start, or the earlier offline edits' conflict refuses the sync and
// the counts never print. Conflicts are closed from the highest number down,
// so closing one never renumbers the ones still to close.
func iSettle(t testing.TB, m *journeys.Machine, c *teamharness.Clone) {
	t.Helper()
	m.LoreIn(c, "sync") // records a refusal as an open conflict
	var open struct {
		Data []json.RawMessage `json:"data"`
	}
	r := m.LoreIn(c, "sync", "conflicts", "--json").OK(t)
	if err := json.Unmarshal([]byte(r.Stdout), &open); err != nil {
		t.Fatalf("sync conflicts --json is not JSON: %v\n%s", err, r)
	}
	for n := len(open.Data); n >= 1; n-- {
		m.LoreIn(c, "sync", "resolve", "--id", strconv.Itoa(n), "--take", "kept", "--yes").OK(t)
	}
	m.LoreIn(c, "sync").OK(t)
}

// iUnderUmask runs fn with every lore of this story started under umask
// mask (a wrapper script sets it, then execs the binary under test), so a
// mode step never changes the umask of the test process the parallel
// stories share. The binary is put back when fn returns.
func iUnderUmask(t *testing.T, s *Story, mask string, fn func()) {
	t.Helper()
	wrapper := filepath.Join(t.TempDir(), "lore-umask-"+mask)
	script := "#!/bin/sh\numask " + mask + "\nexec '" + s.Bin + "' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := s.Bin
	s.Bin = wrapper
	defer func() { s.Bin = bin }()
	fn()
}
