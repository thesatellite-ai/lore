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
		// plain local paths are shown whole (owner report 2026-11-11: a
		// store init hint redacted the middle of a scratch folder path)
		{"path in a hint", `git config --global url."file:///tmp/agent/` + uuid + `/scratchpad/s.git".insteadOf ssh://lore-local/s.git`, nil,
			`git config --global url."file:///tmp/agent/` + uuid + `/scratchpad/s.git".insteadOf ssh://lore-local/s.git`},
		{"path alone", "/tmp/agent/" + uuid + "/scratchpad", nil, "/tmp/agent/" + uuid + "/scratchpad"},
		{"file url", "file:///tmp/agent/" + uuid + "/s.git is a git repository", nil, "file:///tmp/agent/" + uuid + "/s.git is a git repository"},
		{"home path", "see ~/work/" + uuid + "/notes", nil, "see ~/work/" + uuid + "/notes"},
		{"quoted path", `{"dir":"/tmp/` + uuid + `/x"}`, nil, `{"dir":"/tmp/` + uuid + `/x"}`},
		{"windows path", `at C:\Users\dev\` + uuid + `\scratch`, nil, `at C:\Users\dev\` + uuid + `\scratch`},
		{"known id in a path", "/tmp/agent/ses_K7pQ2mZx9/log", []string{"ses_K7pQ2mZx9"}, "/tmp/agent/ses_K7pQ2mZx9/log"},
		{"path then a session", "/tmp/agent/" + uuid + "/x session " + uuid, nil, "/tmp/agent/" + uuid + "/x session " + Redacted},
		// not paths: still masked
		{"one segment", "/" + uuid, nil, "/" + Redacted},
		{"url path", "https://example.com/s/" + uuid + "/x", nil, "https://example.com/s/" + Redacted + "/x"},
		{"scheme-relative", " //example.com/" + uuid + "/x", nil, " //example.com/" + Redacted + "/x"},
		{"ratio-like", "a/" + uuid + "/b", nil, "a/" + Redacted + "/b"},
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
