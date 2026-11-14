//go:build journeys && !windows

package journeys

// oddinput_test.go — odd input gets the exact cause and the next step
// (devco round 4, findings L-3 to L-9). Ada approves a gate nobody asked
// for (it was E_INTERNAL), types a stray word after a switch (it was
// blamed on --title), passes bad JSON to --meta-json (it got the --meta
// grammar), gives a clearing switch with the flag it clears (--due was
// dropped silently), a negative duration (stored, or replaced by the
// default), purges a live task (it asked for --confirm to destroy it),
// types --json=maybe (Go parser text) and an all-zero mission id (called
// "not a msn_ id" while it has that shape). Then (J-7, J-8) task note's
// help promised a push it does not do, and comment add --on-id needed
// --on-table although the id's prefix names the table.

import "testing"

func TestJourneyOddInputAnswers(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona(jAgentsName, jAgentsEmail)
	m := ada.Laptop()
	jAgentsIdentity(t, m)
	app := m.Clone(w.Remote(jAgentsRepo), "app")
	var task string

	w.Step("day1/setup", func() {
		m.LoreIn(app, "init").OK(t)
		mission := jGateID(t, "msn", m.LoreIn(app, "mission", "add", "--title", "odd input").OK(t).Out())
		task = jGateID(t, "tsk", m.LoreIn(app, "task", "add", "--title", "fixture", "--mission", mission).OK(t).Out())
	})

	w.Step("day1/gate-approve-without-a-gate", func() {
		r := app.With("LORE_LIVE_AUTOPUSH=1").Lore("gate", "approve", "--id", task)
		m.results = append(m.results, r)
		r.Fails(t, 23, task+" has no gate to approve", "lore gate request --id "+task).Never(t, "E_INTERNAL")
	})

	w.Step("day1/stray-word-after-a-switch", func() {
		m.LoreIn(app, "task", "add", "--title", "p", "--dry-run", "stray").Fails(t, 20, `unexpected value "stray" after --dry-run (a switch takes no value)`).Never(t, "given twice")
	})

	w.Step("day1/meta-json-says-json", func() {
		m.LoreIn(app, "task", "add", "--title", "p", "--meta-json", "{bad").Fails(t, 20, `--meta-json "{bad" has no key`, `key={"name":"value"}`).Never(t, "has no operator")
		m.LoreIn(app, "task", "add", "--title", "p", "--meta-json", "k={bad").Fails(t, 20, "--meta-json k: the value is not valid JSON", "at byte 2")
	})

	w.Step("day1/clearing-switch-with-its-flag", func() {
		m.LoreIn(app, "task", "edit", "--id", task, "--clear-due", "--due", "2026-12-01").Fails(t, 20, "--clear-due and --due cannot be used together")
		m.LoreIn(app, "memory", "add", "--body", "m", "--no-meta", "--meta", "a=b").Fails(t, 20, "--no-meta goes with --supersedes")
	})

	w.Step("day1/negative-durations", func() {
		m.LoreIn(app, "task", "add", "--title", "p", "--timebox", "-5m").Fails(t, 20, "--timebox must be more than 0")
		m.LoreIn(app, "hold", "claim", "--name", "x", "--ttl", "-1h").Fails(t, 20, "--ttl must be more than 0")
	})

	w.Step("day1/purge-a-live-task", func() {
		for _, extra := range [][]string{nil, {"--dry-run"}} {
			args := append([]string{"trash", "purge", "--id", task}, extra...)
			m.LoreIn(app, args...).Fails(t, 27, task+" is not in the trash (it is alive", "lore task delete --id "+task).Never(t, "--confirm", "nothing to restore:")
		}
	})

	w.Step("day1/task-note-help-says-live-row", func() {
		m.LoreIn(app, "task", "note", "--help").OK(t).Says(t, "a live row; your next `lore sync` or `git push` sends it").Never(t, "pushes your work branch")
	})

	w.Step("day1/comment-on-id-alone", func() {
		m.LoreIn(app, "comment", "add", "--on-id", task, "--body", "the prefix names the table").OK(t).Says(t, "on tasks/"+task)
	})

	w.Step("day1/plain-words-for-values", func() {
		m.LoreIn(app, "sync", "--json=maybe").Fails(t, 20, `--json takes true or false, not "maybe"`).Never(t, "strconv")
		m.LoreIn(app, "task", "add", "--title", "p", "--mission", "msn_00000000000000000000000000000000").Fails(t, 20, "is a malformed msn_ id").Never(t, "is not a msn_ id")
	})
}
