//go:build machines && !windows

package machines

// repos_health_test.go — a setup no check has run for (restored or
// renamed through the trash) says "not checked yet" in setup list, and
// shows its real health after any check run: doctor --all, or a step check
// of its saved answers (setup check, which the page's check calls). A check
// of a draft (other answers) is not the setup's health.

import (
	"encoding/json"
	"strings"
	"testing"
)

// healthRow is setup name's row of the setup list text table.
func healthRow(t *testing.T, m *Machine, name string) string {
	t.Helper()
	r := loreOK(t, m.Lore(t, "", "setup", "list"))
	for _, l := range strings.Split(r.Stdout, "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == name {
			return l
		}
	}
	t.Fatalf("setup list has no row %s: %s", name, r.Stdout)
	return ""
}

// healthJSON is setup name's health in setup list --json ("" = omitted).
func healthJSON(t *testing.T, m *Machine, name string) string {
	t.Helper()
	var v struct {
		Setups []struct {
			Name   string `json:"name"`
			Health string `json:"health"`
		} `json:"setups"`
	}
	if err := json.Unmarshal([]byte(loreOK(t, m.Lore(t, "", "setup", "list", "--json")).Stdout), &v); err != nil {
		t.Fatal(err)
	}
	for _, s := range v.Setups {
		if s.Name == name {
			return s.Health
		}
	}
	t.Fatalf("no setup %s in setup list --json", name)
	return ""
}

const notCheckedCell = "not checked yet"

func TestReposHealthNotCheckedYet(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeReposHistory, loreBin)
	identity := `{"name":"` + PersonName + `","email":"h1@example.test"}`
	loreOK(t, m.Lore(t, "", "setup", "apply", "h1", "--step", "kind", "--answers", `{"kind":"solo","name":"h1","default":false}`))
	loreOK(t, m.Lore(t, "", "setup", "apply", "h1", "--step", "identity", "--answers", identity))
	rename := func(from, to string) {
		t.Helper()
		loreOK(t, m.Lore(t, "", "setup", "remove", from))
		loreOK(t, m.Lore(t, "", "setup", "trash", "restore", from, "--as", to))
	}
	notChecked := func(step, name string) {
		t.Helper()
		if row := healthRow(t, m, name); !strings.Contains(row, notCheckedCell) {
			t.Errorf("[%s] setup list row of a setup never checked: %q", step, row)
		}
		if h := healthJSON(t, m, name); h != "" {
			t.Errorf("[%s] setup list --json health of a setup never checked: %q", step, h)
		}
	}
	checked := func(step, name string) {
		t.Helper()
		if row := healthRow(t, m, name); strings.Contains(row, notCheckedCell) {
			t.Errorf("[%s] setup list still says not checked: %q", step, row)
		}
		if h := healthJSON(t, m, name); h == "" || !strings.ContainsAny(h, "✓⚠✗?") {
			t.Errorf("[%s] setup list --json health: %q", step, h)
		}
	}
	rename("h1", "h2")
	notChecked("restored as h2", "h2")
	// A check of other answers (a draft) is not h2's health.
	loreOK(t, m.Lore(t, "", "setup", "check", "h2", "--step", "identity", "--answers", `{"name":"`+PersonName+`","email":"draft@example.test"}`))
	notChecked("draft check", "h2")
	// A check of the saved answers is (the page's check is the same op).
	loreOK(t, m.Lore(t, "", "setup", "check", "h2", "--step", "identity", "--answers", identity))
	checked("saved check", "h2")
	// Renamed again: never checked under the new name; doctor fills it.
	rename("h2", "h3")
	notChecked("renamed to h3", "h3")
	m.CheckDoctor(t, "health/doctor")
	checked("after doctor --all", "h3")
}
