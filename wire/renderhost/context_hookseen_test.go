package renderhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHookSeen: a session's set remembers what was sent; another session
// and a call without a session id start empty (CONTEXT_BUDGET 6.9).
func TestHookSeen(t *testing.T) {
	home := t.TempDir()
	s := loadHookSeen(home, "claude_code", "sess-1")
	if len(s.ids) != 0 {
		t.Fatalf("fresh set = %v", s.ids)
	}
	if err := s.add([]string{"rul_a", "hfx_b", "rul_a"}); err != nil {
		t.Fatal(err)
	}
	again := loadHookSeen(home, "claude_code", "sess-1")
	if !again.ids["rul_a"] || !again.ids["hfx_b"] || len(again.ids) != 2 {
		t.Fatalf("reloaded set = %v", again.ids)
	}
	if other := loadHookSeen(home, "claude_code", "sess-2"); len(other.ids) != 0 {
		t.Fatalf("other session = %v", other.ids)
	}
	none := loadHookSeen(home, "claude_code", "")
	if err := none.add([]string{"rul_a"}); err != nil || none.file != "" {
		t.Fatalf("no-session set = %+v %v", none, err)
	}
	r := contextResult{picks: []contextPick{{ID: "rul_a"}, {ID: "rul_c"}}}.without(again.ids)
	if len(r.picks) != 1 || r.picks[0].ID != "rul_c" {
		t.Fatalf("without = %+v", r.picks)
	}
}

// TestHookFilesUnderLoreHome: the hook's seen-set and its error log live
// under the lore home the host was given, never a fixed ~/.lore.
func TestHookFilesUnderLoreHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if got := hookSeenFile(home, "claude_code", "s"); !strings.HasPrefix(got, filepath.Join(home, "state", "hook-seen")+string(filepath.Separator)) {
		t.Fatalf("seen file %q is not under %s", got, home)
	}
	if got := hookSeenFile("", "claude_code", "s"); got != "" {
		t.Fatalf("no home: %q", got)
	}
	h := New(Options{Home: home})
	h.logHookError(os.ErrNotExist)
	b, err := os.ReadFile(filepath.Join(home, "logs", "hooks.log"))
	if err != nil || !strings.Contains(string(b), "lore context --hook: file does not exist") {
		t.Fatalf("hooks.log = %q, %v", b, err)
	}
}

// TestHooksLogRotates: past its cap the log moves to hooks.log.1 and a new
// one starts; at most one old copy is kept.
func TestHooksLogRotates(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, filepath.FromSlash(HooksLogFile))
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	appendHooksLog(home, now, "first")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", hooksLogMax)), 0o600); err != nil {
		t.Fatal(err)
	}
	appendHooksLog(home, now, "after the cap")
	cur, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(cur), "xxx") || !strings.Contains(string(cur), "after the cap") {
		t.Fatalf("new log = %q, %v", cur, err)
	}
	old, err := os.Stat(path + hooksLogOldSuffix)
	if err != nil || old.Size() != hooksLogMax {
		t.Fatalf("rotated copy: %v %v", old, err)
	}
}
