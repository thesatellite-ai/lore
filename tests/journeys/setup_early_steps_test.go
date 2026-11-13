//go:build journeys && !windows

package journeys

// setup_early_steps_test.go — an admin answers the setup page out of order
// (the page lets every step after kind be saved in any order): tools,
// secrets and folders before the team step. Those steps found no entry to
// write yet (an admin entry exists only once its org does) and were
// reported done but dropped: the finished setup had no folders, no tools
// and default false. Along the way the team step is first given an own
// pattern with a wildcard host: setup check passed it and apply refused it
// only after cloning the team store. The github step is skipped (the
// stores are reached as git is configured), so no GitHub check may run.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneySetupEarlySteps(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	kim := tlPerson{"Kim Ardent", "kim@example.test", "kim-ardent"}
	m := tt.person(kim)
	dir := tt.repo[kim.Email].Dir
	const setup, org = "ardent", "ardent"
	storeURL := tt.store.SSHURL()

	w.Step("day1/kim-saves-the-later-steps-before-the-team", func() {
		tlApply(t, m, dir, setup, "kind", map[string]any{"kind": "admin", "name": setup})
		tlApply(t, m, dir, setup, "identity", map[string]any{"name": kim.Name, "email": kim.Email})
		tlApply(t, m, dir, setup, "github", map[string]any{})
		tlApply(t, m, dir, setup, "personal_store", map[string]any{"personal_store": "local"})
		tlApply(t, m, dir, setup, "tools", map[string]any{"tools": []string{"claude_code"}})
		tlApply(t, m, dir, setup, "secrets", map[string]any{"backend": "env"})
		tlApply(t, m, dir, setup, "folders", map[string]any{"folders": []string{tlFolders}, "default": true})
	})

	w.Step("day1/a-wildcard-host-pattern-is-refused-before-any-clone", func() {
		bad := tlAnswers(t, map[string]any{"org": org, "store_url": storeURL, "own": []string{"*/app"}})
		m.Lore(dir, "setup", "check", setup, "--step", "team", "--answers", bad, "--json").OK(t).
			Says(t, "own", "repo_pattern", "may not use a wildcard for the host", "github.com/agency/*")
		m.Lore(dir, "setup", "apply", setup, "--step", "team", "--answers", bad, "--yes").Refused(t).
			Says(t, "E_INVALID_INPUT", "may not use a wildcard for the host").Never(t, "nothing changed")
		if ents, _ := os.ReadDir(filepath.Join(m.Home, ".lore", "stores", "acme-co")); len(ents) > 0 {
			t.Errorf("the refused team step cloned the team store: %v", ents)
		}
	})

	w.Step("day1/the-team-step-writes-every-earlier-answer", func() {
		tlApply(t, m, dir, setup, "team", map[string]any{"org": org, "store_url": storeURL, "own": []string{"github.com/acme-co/*"}})
		tlApply(t, m, dir, setup, "summary", map[string]any{})
		cfg, err := os.ReadFile(filepath.Join(m.Home, ".lore", "config.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"default: true", tlFolders, "claude_code", "backend: env"} {
			if !strings.Contains(string(cfg), want) {
				t.Errorf("config.yaml lacks %q after the team step:\n%s", want, cfg)
			}
		}
		tt.lore(kim, "setup", "list").OK(t).Says(t, setup, "admin", org, "true", tlFolders)
	})

	w.Step("day2/a-skipped-github-step-is-never-checked", func() {
		w.AdvanceDays(1)
		tt.lore(kim, "setup", "repair", setup).OK(t).Says(t, "nothing to repair").Never(t, "github")
		tt.lore(kim, "doctor", "--all", "--fresh", "--json").Says(t, "the github step was skipped").Never(t, "GitHub refused the key")
	})
}
