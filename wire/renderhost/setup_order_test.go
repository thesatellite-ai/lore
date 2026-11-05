package renderhost

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// TestWriteHookFileKeepsKeyOrder (OV11): lore setup --agent-hooks adds and
// removes its hook without reordering the user's settings file: the
// top-level keys, the hooks object's events and a user group's keys stay
// where the user put them (not sorted), a new key goes last, and remove
// gives back the same key order it started with.
func TestWriteHookFileKeepsKeyOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	user := `{
  "permissions": {"deny": ["Read(./.env)"], "allow": ["Bash(ls)"]},
  "env": {"Z": "1", "A": "2"},
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "say done"}], "matcher": ""}],
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "guard"}]}]
  },
  "model": "opus"
}
`
	if err := os.WriteFile(path, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	h := toolprofile.SessionHook{Tool: "claude_code", ConfigFile: "settings.json", Event: "SessionStart"}
	order := func() []string {
		b, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		var at []string
		for _, k := range []string{`"permissions"`, `"deny"`, `"allow"`, `"env"`, `"Z"`, `"A"`, `"hooks"`, `"Stop"`, `"say done"`, `"matcher": ""`, `"PreToolUse"`, `"SessionStart"`, `"model"`} {
			if strings.Contains(s, k) {
				at = append(at, k)
			}
		}
		slices.SortStableFunc(at, func(a, b string) int { return strings.Index(s, a) - strings.Index(s, b) })
		return at
	}
	before := order()
	if err := writeHookFile(path, h, false); err != nil {
		t.Fatal(err)
	}
	want := []string{`"permissions"`, `"deny"`, `"allow"`, `"env"`, `"Z"`, `"A"`, `"hooks"`, `"Stop"`, `"say done"`, `"matcher": ""`, `"PreToolUse"`, `"SessionStart"`, `"model"`}
	if got := order(); !slices.Equal(got, want) {
		t.Fatalf("after add: %v\nwant %v", got, want)
	}
	if ok, err := hookInstalled(path, h); err != nil || !ok {
		t.Fatalf("hook not installed: %v %v", ok, err)
	}
	if err := writeHookFile(path, h, true); err != nil {
		t.Fatal(err)
	}
	if got := order(); !slices.Equal(got, before) {
		t.Fatalf("after remove: %v\nwant %v", got, before)
	}
	if ok, _ := hookInstalled(path, h); ok {
		t.Fatal("hook still installed")
	}
}
