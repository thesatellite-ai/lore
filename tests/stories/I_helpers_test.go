//go:build stories && !windows

package stories

// I_helpers_test.go — small helpers of story I (two machines, text,
// environment). Prefixed i… so the other stories' helper files in this
// package never clash with them.

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// iRowID matches the first row id a command prints.
var iRowID = regexp.MustCompile(`(mem|dec|rul|tsk)_[0-9a-f]{32}`)

// iActorID matches an actor id.
var iActorID = regexp.MustCompile(`act_[0-9a-f]{32}`)

// iWaiting runs a step whose right behaviour waits on an open finding,
// written as it SHOULD behave and skipped with the finding id (see
// hWaiting in story H): a subtest, so the skip ends only that step.
func iWaiting(t *testing.T, finding string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(finding, func(t *testing.T) {
		t.Skip("waiting for " + finding)
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
