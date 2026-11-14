//go:build journeys && !windows

package journeys

// solo_volume_test.go — a long-lived repo (bug hunt G-6, G-7, G-9). After
// months of notes Sam has more must rules than LORE.md's budget holds in
// full, written the way most people write them: one long sentence, no
// --summary. Every rule still says what it requires in LORE.md (cut at a
// word, never a bare id with the same `lore show` twice). Sam retires a
// month of memories with one `memory archive --where` (dry run first, one
// batch, undone with `lore batch undo`), and `lore doctor` shows the
// LORE.md size as information, not under a warnings heading.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// volReason makes each rule one long sentence (no ". "), so the old line
// form had no first sentence to show; three of them make 45 rules
// overflow a 32000-byte budget.
const volReason = " because the release pipeline depends on it and every reviewer must be able to trace the change back to the incident report without guessing which service owns the endpoint or which team was paged during the outage window"

var volBatch = regexp.MustCompile(`bat_[0-9a-f]{32}`)

func TestJourneySoloVolume(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	app := w.Remote("github.com/sam-dev/volume-app")
	w.Remote(soloStore)
	var repo *teamharness.Clone
	const rules = 45

	w.Step("day1/setup", func() {
		lap.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		soloSetupStart(t, lap, soloName, soloEmail)
		soloSetupRest(t, lap, "create")
		repo = lap.Clone(app, "volume-app")
		lap.LoreIn(repo, "init").OK(t)
	})

	w.Step("month6/many-long-must-rules-still-read-in-LORE.md", func() {
		for i := 1; i <= rules; i++ {
			lap.LoreIn(repo, "rule", "add", "--severity", "must", "--body", "Rule "+strconv.Itoa(i)+" for the billing area: run the migration check"+volReason+volReason+volReason).OK(t)
		}
		lap.LoreIn(repo, "render").OK(t).Says(t, "line rules")
		raw, err := os.ReadFile(filepath.Join(repo.Dir, ".lore", "LORE.md"))
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		if strings.Contains(body, "no short form") {
			t.Errorf("LORE.md still has bare-id rules:\n%s", body)
		}
		lines := 0
		for _, l := range strings.Split(body, "\n") {
			if !strings.HasPrefix(l, "- `rul_") {
				continue
			}
			lines++
			if !strings.Contains(l, " for the billing area: run the migration check") || strings.Contains(l, "lore show") {
				t.Errorf("a rule line does not say what it requires, or repeats lore show: %s", l)
			}
		}
		if lines == 0 {
			t.Fatalf("no rule was a line; the journey needs more rules than the budget holds:\n%s", body)
		}
		if strings.Count(body, "`lore show --id <id>` prints the full text") != 1 {
			t.Errorf("LORE.md does not say once how to see a line's full text")
		}
	})

	w.Step("month7/retire-a-month-of-memories-in-one-batch", func() {
		for i := 1; i <= 6; i++ {
			lap.LoreIn(repo, "memory", "add", "--body", "month one note "+strconv.Itoa(i)).OK(t)
		}
		lap.LoreIn(repo, "memory", "add", "--body", "month two note").OK(t)
		lap.LoreIn(repo, "memory", "archive", "--where", "body~=month one", "--dry-run").OK(t).
			Says(t, "would archive 6 memory(s):", "nothing written").Never(t, "month two")
		out := lap.LoreIn(repo, "memory", "archive", "--where", "body~=month one", "--yes").OK(t).
			Says(t, "archived 6 memory(s)", "lore batch undo --id bat_")
		bat := volBatch.FindString(out.Stdout + out.Stderr)
		lap.LoreIn(repo, "memory", "list").OK(t).Says(t, "month two note").Never(t, "month one note")
		lap.LoreIn(repo, "batch", "undo", "--id", bat, "--yes").OK(t)
		lap.LoreIn(repo, "memory", "list").OK(t).Says(t, "month one note 1", "month one note 6")
	})

	w.Step("month7/doctor-keeps-the-size-line-out-of-the-warnings", func() {
		lap.LoreIn(repo, "render").OK(t)
		r := lap.LoreIn(repo, "doctor")
		text := r.Stdout + r.Stderr
		if strings.Contains(text, "\nContext:\n") {
			t.Errorf("doctor prints a bare Context: list that reads as more warnings:\n%s", text)
		}
		if strings.Contains(text, "of 16000 bytes") || strings.Contains(text, "of 32000 bytes") {
			if !strings.Contains(text, "Context (information, nothing to fix):") && !strings.Contains(text, "Context warnings:") {
				t.Errorf("doctor's LORE.md size line has no heading of its own:\n%s", text)
			}
		}
	})
}
