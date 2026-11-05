package renderhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// TestDoubleLoads (CONTEXT_BUDGET 8.6; fa-ctx later:46): one warning when a
// target's pointer file imports another target's pointer file, naming the
// tool that loads LORE.md twice; none for one target, for an import of a
// file that is not a target's, for a mention that is not an @ line, or for
// a missing pointer file.
func TestDoubleLoads(t *testing.T) {
	claude, codex, gemini := builtin(t, toolprofile.NameClaudeCode), builtin(t, toolprofile.NameCodex), builtin(t, toolprofile.NameGeminiCLI)
	cases := []struct {
		name    string
		claude  string // CLAUDE.md content ("" = no file)
		targets []toolprofile.Profile
		want    int
	}{
		{"imports codex's pointer", "@.lore/LORE.md\n@AGENTS.md\n", []toolprofile.Profile{claude, codex}, 1},
		{"one target", "@AGENTS.md\n", []toolprofile.Profile{claude}, 0},
		{"imports a non-target", "@AGENTS.md\n", []toolprofile.Profile{claude, gemini}, 0},
		{"mention only", "see AGENTS.md and @AGENTS.md.\n", []toolprofile.Profile{claude, codex}, 0},
		{"no file", "", []toolprofile.Profile{claude, codex}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if c.claude != "" {
				if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(c.claude), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var ts []contextTarget
			for _, p := range c.targets {
				ts = append(ts, contextTarget{profile: p})
			}
			got := doubleLoads(root, ts)
			if len(got) != c.want {
				t.Fatalf("warnings %v, want %d", got, c.want)
			}
			if c.want == 1 && !strings.Contains(got[0], "CLAUDE.md imports AGENTS.md") || c.want == 1 && !strings.Contains(got[0], "claude_code loads LORE.md twice") {
				t.Fatalf("warning %q", got[0])
			}
		})
	}
}

// TestDirFileCount (8.9; fa-ctx later:46): only folder scopes of this repo
// whose LORE.md carries render's header count; a hand-written LORE.md and
// a folder without a file do not.
func TestDirFileCount(t *testing.T) {
	h := opstest.New(t)
	meta := filepath.Join(h.Repo, rowfs.LoreDir, rowfs.DataDir)
	if err := os.MkdirAll(meta, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meta, rowfs.MetaFileName), []byte(`{"_v":1,"project_id":"`+h.Project+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.App.EnsureRepoIdentity(context.Background()); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"a": dirHeaderPrefix + "a; budget 1 bytes; do not edit -->\n", "b": "# my own notes\n", "c": "", "d/e": dirHeaderPrefix + "d/e -->\n"}
	for d, body := range files {
		if err := os.MkdirAll(filepath.Join(h.Repo, filepath.FromSlash(d)), 0o750); err != nil {
			t.Fatal(err)
		}
		h.Must("rule.add", `{"body":"r","severity":"must","scope":"dir:`+d+`"}`)
		if body != "" {
			if err := os.WriteFile(filepath.Join(h.Repo, filepath.FromSlash(d), dirFileName), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n := New(Options{App: h.App}).dirFileCount(context.Background(), h.Repo); n != 2 {
		t.Fatalf("dirFileCount %d, want 2", n)
	}
}
