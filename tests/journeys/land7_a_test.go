//go:build journeys && !windows

package journeys

// land7_a_test.go — the solo dev's knowledge-base store and the end of a
// coding session (decided 2026-11-10, land7 A):
//
//	day 1    Sam attaches a lore-only store kb to his repo (.lore/stores.yaml),
//	         installs the coding tools' hooks (start AND end), switches kb to
//	         push=batch; two notes wait on the laptop; Claude Code's session
//	         ends and its end hook pushes them as one batch commit, silently
//	day 2    the train: offline, a note, the session ends; the hook still exits
//	         0 at once with no output, the note stays safe; online, `lore sync`
//	         pushes it
//	week 1   Sam removes the hooks: start and end go together
//
// Named TestJourneySolo… so the solo persona's gate step runs it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Placeholders of this journey.
const (
	l7aName    = "Sam Solo"
	l7aEmail   = "sam@solo.example"
	l7aRepo    = "github.com/sam-dev/notes-app"
	l7aKB      = "github.com/sam-dev/kb"
	l7aKBName  = "kb"
	l7aClaude  = "claude_code"
	l7aBatch   = "lore: batch "
	l7aHookCmd = "lore agent end --tool " + l7aClaude
)

// l7aHooks is every command lore's Claude Code settings file runs per event.
func l7aHooks(t testing.TB, repoDir string) map[string][]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoDir, ".claude", "settings.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf(".claude/settings.json: %v", err)
	}
	out := map[string][]string{}
	for ev, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				out[ev] = append(out[ev], h.Command)
				if h.Command == l7aHookCmd && h.Timeout < 20 {
					t.Errorf("the end hook's timeout is %ds: Claude Code would cut its push off", h.Timeout)
				}
			}
		}
	}
	return out
}

// l7aRemoteHas reports whether kb's main holds a memory containing body.
func l7aRemoteHas(w *World, body string) bool {
	r := w.Remote(l7aKB)
	for _, f := range r.FilesMatching("main", ".lore/data/memories/") {
		if s, ok := r.Read("main", f); ok && strings.Contains(s, body) {
			return true
		}
	}
	return false
}

func TestJourneySoloKBStoreSessionEnd(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(l7aName, l7aEmail)
	m := sam.Laptop()
	kb := w.Remote(l7aKB)
	app := m.Clone(w.Remote(l7aRepo), "notes-app")

	w.Step("day1/attach-kb-and-install-hooks", func() {
		m.LoreIn(app, "init").OK(t)
		m.LoreIn(app, "store", "init", l7aKBName, kb.URL()).OK(t)
		if err := os.MkdirAll(filepath.Join(app.Dir, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		m.LoreIn(app, "setup", "--agent-hooks", "--yes").OK(t)
		hooks := l7aHooks(t, app.Dir)
		if !slices.Contains(hooks["SessionStart"], "lore agent start --tool "+l7aClaude+" --hook-stdin") || !slices.Contains(hooks["SessionEnd"], l7aHookCmd) {
			t.Errorf("setup --agent-hooks must install the start and the end hook, got %v", hooks)
		}
	})

	w.Step("day1/kb-batches-its-writes", func() {
		// kb is not the personal store and belongs to no org: before land7
		// A this was refused ("no admin can be named").
		m.LoreIn(app, "store", "set", l7aKBName, "push=batch", "push_every=1h").OK(t).
			Says(t, "kb: push batch every 1h", "the store's lore-store.yaml")
		m.LoreIn(app, "memory", "add", "--body", "kb: the staging db resets nightly", "--to", l7aKBName).OK(t)
		m.LoreIn(app, "memory", "add", "--body", "kb: invoices go out on the 1st", "--to", l7aKBName).OK(t)
		if l7aRemoteHas(w, "staging db resets") || l7aRemoteHas(w, "invoices go out") {
			t.Errorf("batch mode pushed a write before push_every")
		}
		var show struct {
			Push struct {
				Place    string `json:"place"`
				Unpushed int    `json:"unpushed"`
			} `json:"push"`
		}
		r := m.LoreIn(app, "store", "show", l7aKBName, "--json").OK(t)
		if err := json.Unmarshal([]byte(r.Stdout), &show); err != nil || show.Push.Place != "store" || show.Push.Unpushed < 2 {
			t.Errorf("store show kb: want place store with the two writes waiting, got %+v (%v)", show.Push, err)
		}
	})

	w.Step("day1/claude-session-ends", func() {
		before := kb.Log("main")
		r := m.LoreIn(app, "agent", "end", "--tool", l7aClaude)
		if r.Code != 0 || r.Stdout != "" || r.Stderr != "" {
			t.Errorf("the end hook must exit 0 silently, got %s", r)
		}
		after := kb.Log("main")
		if len(after) != len(before)+1 || !strings.HasPrefix(after[0], l7aBatch) {
			t.Errorf("the end hook must push the waiting writes as ONE batch commit:\nbefore %q\nafter  %q", before, after)
		}
		if !l7aRemoteHas(w, "staging db resets") || !l7aRemoteHas(w, "invoices go out") {
			t.Errorf("after the session ended, kb on GitHub lacks the batched notes")
		}
	})

	w.AdvanceDays(1)
	w.Step("day2/session-ends-on-the-train", func() {
		m.Offline()
		m.LoreIn(app, "memory", "add", "--body", "kb: written on the train", "--to", l7aKBName).OK(t)
		start := time.Now()
		r := m.LoreIn(app, "agent", "end", "--tool", l7aClaude)
		if r.Code != 0 || r.Stdout != "" || r.Stderr != "" {
			t.Errorf("offline, the end hook must still exit 0 silently, got %s", r)
		}
		if d := time.Since(start); d > 25*time.Second {
			t.Errorf("offline, the end hook took %s (bounded by the 15 s push limit)", d)
		}
		if l7aRemoteHas(w, "written on the train") {
			t.Errorf("an offline machine pushed")
		}
		m.Online()
		m.LoreIn(app, "sync").OK(t)
		if !l7aRemoteHas(w, "written on the train") {
			t.Errorf("back online, `lore sync` must push the note the offline session end left waiting")
		}
	})

	w.AdvanceDays(5)
	w.Step("week1/remove-hooks", func() {
		m.LoreIn(app, "setup", "--agent-hooks", "--remove", "--yes").OK(t)
		hooks := l7aHooks(t, app.Dir)
		for ev, cmds := range hooks {
			for _, c := range cmds {
				if strings.HasPrefix(c, "lore ") {
					t.Errorf("--remove left lore's %s hook %q", ev, c)
				}
			}
		}
	})
}
