//go:build journeys && !windows

package journeys

// errsteps_test.go — every refusal names its cause and the next step
// (devco L-16), and an empty LORE.md names only the writes that pin
// (devco L-18). Nina mistypes a plan path, forgets who a handoff is for,
// marks an open task merged and gives two placement flags; each answer
// says what was wrong and the command that fixes it.

import (
	"strings"
	"testing"
)

func TestJourneyErrorsNameTheNextStep(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nina := w.Persona("Nina Newcomer", "nina@example.test")
	app := nina.Laptop().Clone(w.Remote("github.com/nina/shop"), "shop")

	w.Step("day1/refusals-name-the-next-step", func() {
		app.Lore("init", "--non-interactive", "--name=shop").OK(t)
		app.Lore("mission", "apply", "--file", "plans/launch.yaml").
			Fails(t, 20, "mission apply: cannot read plans/launch.yaml: no such file or directory", "hint: check the path of --file", "lore mission apply --help").
			Never(t, "read plan\n")
		task := jGateID(t, "tsk", app.Lore("task", "add", "--title", "ship the cart").OK(t).Out())
		app.Lore("handoff", "add", "--body", "cart half done", "--task", task).
			Fails(t, 20, "--for @handle is required", "hint: name who picks it up: lore handoff add --for @handle --body")
		app.Lore("task", "merged", "--id", task).
			Fails(t, 20, "is todo; only a done task can be merged", "hint: finish it first with lore task done --id "+task)
		app.Lore("task", "edit", "--id", task, "--first", "--last").
			Fails(t, 20, "give only one", "hint: keep one: --first, --last, --before <tsk_id> or --after-sibling <tsk_id>")
	})

	w.Step("day1/empty-lore-md-names-what-pins", func() {
		app.Lore("memory", "add", "--body", "the cart total rounds per line").OK(t)
		app.Lore("render").OK(t)
		md, ok := app.ReadFile(".lore/LORE.md")
		if !ok {
			t.Fatal("no .lore/LORE.md after render")
		}
		_, line, found := strings.Cut(md, "_No pinned knowledge yet.")
		if !found {
			t.Fatalf("no empty-state line in LORE.md:\n%s", md)
		}
		line, _, _ = strings.Cut(line, "\n")
		if !strings.Contains(line, "A must rule (`lore rule add") || !strings.Contains(line, "`lore hotfix add") {
			t.Errorf("empty-state line names no pinning write: %q", line)
		}
		if strings.Contains(line, "memory add") {
			t.Errorf("empty-state line tells people a memory pins: %q", line)
		}
	})
}
