//go:build journeys && !windows

package journeys

// flagzero_test.go — a typed 0 is never silently the default (bug hunt
// G-20). Nina asks for every open task with no change "for 0 days"; lore
// used to run the 60-day default and say "no change for 60 days". Now a
// flag whose 0 means "not given" refuses the 0 and names the range, and
// leaving the flag out still takes the default.

import "testing"

func TestJourneyFlagZero(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nina := w.Persona("Nina Newcomer", "nina@example.test")
	app := nina.Laptop().Clone(w.Remote("github.com/nina/shop"), "shop")

	w.Step("day1/stale-days-zero-refused", func() {
		app.Lore("init", "--non-interactive", "--name=shop").OK(t)
		app.Lore("task", "add", "--title", "ship the cart").OK(t)
		app.Lore("task", "stale", "--days", "0").Fails(t, 20, "--days must be 1 or more", "leave --days out").Never(t, "60 days")
		app.Lore("task", "stale").OK(t).Says(t, "no change for 60 days")
		app.Lore("task", "stale", "--days", "1").OK(t).Says(t, "no change for 1 days")
		app.Lore("task", "list", "--page", "0").Fails(t, 20, "--page must be 1 or more")
	})
}
