package renderhost

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
)

// TestHookSeen: a session's set remembers what was sent; another session
// and a call without a session id start empty (CONTEXT_BUDGET 6.9).
func TestHookSeen(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	s := loadHookSeen("claude_code", "sess-1")
	if len(s.ids) != 0 {
		t.Fatalf("fresh set = %v", s.ids)
	}
	if err := s.add([]string{"rul_a", "hfx_b", "rul_a"}); err != nil {
		t.Fatal(err)
	}
	again := loadHookSeen("claude_code", "sess-1")
	if !again.ids["rul_a"] || !again.ids["hfx_b"] || len(again.ids) != 2 {
		t.Fatalf("reloaded set = %v", again.ids)
	}
	if other := loadHookSeen("claude_code", "sess-2"); len(other.ids) != 0 {
		t.Fatalf("other session = %v", other.ids)
	}
	none := loadHookSeen("claude_code", "")
	if err := none.add([]string{"rul_a"}); err != nil || none.file != "" {
		t.Fatalf("no-session set = %+v %v", none, err)
	}
	r := contextResult{picks: []contextPick{{ID: "rul_a"}, {ID: "rul_c"}}}.without(again.ids)
	if len(r.picks) != 1 || r.picks[0].ID != "rul_c" {
		t.Fatalf("without = %+v", r.picks)
	}
}
