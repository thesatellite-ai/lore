package gitcommit

import "testing"

// TestCIRunTrailer pins OQ13's trailer and its absence outside CI.
func TestCIRunTrailer(t *testing.T) {
	env := map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REPOSITORY": "acme/x", "GITHUB_RUN_ID": "123456789", "GITHUB_RUN_ATTEMPT": "1",
		"GITHUB_WORKFLOW": "ci", "GITHUB_JOB": "lore-check", "GITHUB_SHA": "4e6ed1c99"}
	got := AppendTrailers("lore: live update", "Lore-Version: 0.1.18", CIRunTrailer(func(k string) string { return env[k] }))
	want := "lore: live update\n\nLore-Version: 0.1.18\nLore-CI-Run: github_actions run 123456789 attempt 1, workflow ci, job lore-check, commit 4e6ed1c"
	if got != want {
		t.Fatalf("got %q", got)
	}
	if CIRunTrailer(func(string) string { return "" }) != "" || AppendTrailers("m", "") != "m" {
		t.Fatal("a trailer outside CI")
	}
}
