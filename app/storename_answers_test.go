package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWriteAnswersNameTheStore: task watch/unwatch, meta set/unset and
// rule/decision/hotfix archive/delete answered with store "" (the row was
// written, but the answer could not say where). Each now names the store
// it wrote to: "repo" in a plain repo.
func TestWriteAnswersNameTheStore(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t)
	w.person().author()
	repo := w.initRepo(t)
	idOf := func(args ...string) string {
		t.Helper()
		code, out, errOut := w.lore(t, repo, append(args, "--json")...)
		var r struct {
			ID   string `json:"id"`
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &r) != nil {
			t.Fatalf("lore %s: exit %d %s%s", strings.Join(args, " "), code, out, errOut)
		}
		if r.ID == "" {
			r.ID = r.Data.ID
		}
		return r.ID
	}
	task := idOf("task", "add", "--title", "watch me", "--commitment", "accepted")
	rule := idOf("rule", "add", "--body", "always check the input")
	dec := idOf("decision", "add", "--title", "one way", "--body", "because")
	hfx := idOf("hotfix", "add", "--title", "beware", "--body", "it bites", "--severity", "high")
	for _, args := range [][]string{
		{"task", "watch", "--id", task},
		{"task", "unwatch", "--id", task},
		{"meta", "set", "--id", task, "--pair", "k=v"},
		{"meta", "unset", "--id", task, "--key", "k"},
		{"rule", "archive", "--id", rule},
		{"decision", "archive", "--id", dec},
		{"hotfix", "archive", "--id", hfx},
		{"rule", "delete", "--id", rule, "--yes"},
		{"decision", "delete", "--id", dec, "--yes"},
		{"hotfix", "delete", "--id", hfx, "--yes"},
	} {
		code, out, errOut := w.lore(t, repo, append(args, "--json")...)
		var r struct {
			Store string `json:"store"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &r) != nil {
			t.Errorf("lore %s: exit %d %s%s", strings.Join(args, " "), code, out, errOut)
			continue
		}
		if r.Store != "repo" {
			t.Errorf("lore %s --json: store %q, want \"repo\": %s", strings.Join(args, " "), r.Store, out)
		}
	}
}
