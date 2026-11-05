package sessionmask

import (
	"slices"
	"strings"
	"testing"
)

// uuid is a Claude Code / Codex shaped session id (built at run time so
// no fixture looks like a credential).
var uuid = strings.Join([]string{"0f8b2c1e", "7d3a", "4b6c", "9e10", "a1b2c3d4e5f6"}, "-")

// TestMaskMatrix: shape x position x case x neighbours x known ids. Every
// session-shaped token is masked without any known id (PASS3-A-7: the mask
// never depends on lore.db); lore's own ids and longer hex runs are not.
func TestMaskMatrix(t *testing.T) {
	t.Parallel()
	loreID := "mem_0190abcd0123456789abcdef01234567"
	cases := []struct {
		name, in string
		known    []string
		want     string
	}{
		{"alone", uuid, nil, Redacted},
		{"upper", strings.ToUpper(uuid), nil, Redacted},
		{"in prose", "session " + uuid + " ended", nil, "session " + Redacted + " ended"},
		{"line start/end", uuid + "\n" + uuid, nil, Redacted + "\n" + Redacted},
		{"json string", `{"s":"` + uuid + `"}`, nil, `{"s":"` + Redacted + `"}`},
		{"amp prefix", "T-" + uuid, nil, "T-" + Redacted},
		{"adjacent pair", uuid + " " + uuid, nil, Redacted + " " + Redacted},
		{"glued by one char", uuid + "," + uuid, nil, Redacted + "," + Redacted},
		{"unicode around", "→" + uuid + "←", nil, "→" + Redacted + "←"},
		{"lore id untouched", loreID, nil, loreID},
		{"longer hex run untouched", uuid + "ab", nil, uuid + "ab"},
		{"hex before untouched", "a" + uuid, nil, "a" + uuid},
		{"empty", "", nil, ""},
		{"known non-uuid", "id ses_K7pQ2mZx9 here", []string{"ses_K7pQ2mZx9"}, "id " + Redacted + " here"},
		{"known too short ignored", "abc short", []string{"abc"}, "abc short"},
		{"known nested longest first", "xx_12345678_tail", []string{"12345678", "xx_12345678_tail"}, Redacted},
		{"known blank ignored", "plain", []string{"", "   "}, "plain"},
	}
	for _, c := range cases {
		if got := Mask(c.in, c.known); got != c.want {
			t.Errorf("%s: Mask(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestEnvIDs: only the allowlisted session variables are read, and each
// built-in detector's session variable is among them.
func TestEnvIDs(t *testing.T) {
	t.Parallel()
	names := EnvNames()
	for _, n := range []string{EnvLoreSession, "CLAUDE_CODE_SESSION_ID", "CODEX_SESSION_ID", "AMP_CURRENT_THREAD_ID"} {
		if !slices.Contains(names, n) {
			t.Errorf("EnvNames lacks %s", n)
		}
	}
	read := map[string]bool{}
	env := map[string]string{"CODEX_SESSION_ID": " thr_abcdefgh ", "HOME": "/home/x", "LORE_SESSION": ""}
	got := EnvIDs(func(k string) string { read[k] = true; return env[k] })
	if !slices.Equal(got, []string{"thr_abcdefgh"}) {
		t.Fatalf("EnvIDs = %q", got)
	}
	if read["HOME"] {
		t.Fatal("EnvIDs read a variable outside the allowlist")
	}
}
