package renderhost

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/wire"
)

// TestClaudeStartWarnings (CQ8 decision A, later:454) replays the fixture
// repos measured on Claude Code 2.1.318 with a 200K-window model (the
// numbers next to claudeStartFileMax): a file of 39,999 or exactly 40,000
// bytes is quiet and 40,001 warns; three files of 39,000 (117,000 plus the
// pointer) are quiet, four of 35,000 plus the local file warn on the
// total; a 151,000-byte LORE.md warns on the file and is left out of the
// total, as the tool does; the user-level ~/.claude/CLAUDE.md counts; a
// private store file imported by CLAUDE.local.md counts on its own. The
// launch-set note keeps its v0.1.17 shape.
func TestClaudeStartWarnings(t *testing.T) {
	claude := builtin(t, toolprofile.NameClaudeCode)
	write := func(t *testing.T, path string, n int) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Repeat("x", n)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const pointer = "@.lore/LORE.md\n" // 15 bytes, as in the fixtures
	cases := []struct {
		name            string
		lore, local     int // 0 = no CLAUDE.local.md
		user, priv      int // ~/.claude/CLAUDE.md and one private file (0 = none)
		fileWarn, total bool
	}{
		{name: "39,999 quiet", lore: 39999},
		{name: "40,000 quiet", lore: 40000},
		{name: "40,001 warns", lore: 40001, fileWarn: true},
		{name: "117,000 in three files quiet", lore: 39000, local: 39000, priv: 39000},
		{name: "140,000 in four files warns on the total", lore: 35000, local: 35000, priv: 35000, user: 35000, total: true},
		{name: "151,000 file left out of the total", lore: 151000, local: 20000, fileWarn: true},
		{name: "user-level file counts", lore: 39000, local: 39000, user: 39000, priv: 4000, total: true},
		{name: "exactly 120,000 quiet", lore: 40000, local: 40000, priv: 40000 - len(pointer)},
		{name: "120,001 warns", lore: 40000, local: 40000, priv: 40001 - len(pointer), total: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			testenv.SetHome(t, home)
			if c.user > 0 {
				write(t, filepath.Join(home, ".claude", "CLAUDE.md"), c.user)
			}
			if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(pointer), 0o600); err != nil {
				t.Fatal(err)
			}
			if c.local > 0 {
				body := strings.Repeat("y", c.local)
				if c.priv > 0 {
					priv := filepath.Join(home, renderDirName, "rep", "sto.md")
					write(t, priv, c.priv)
					imp := "@" + priv + "\n"
					body = imp + body[len(imp):]
				}
				if err := os.WriteFile(filepath.Join(root, "CLAUDE.local.md"), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := claudeStartWarnings(launchFiles(root, claude, c.lore))
			var file, total int
			for _, w := range got {
				switch {
				case strings.Contains(w, "per file"):
					file++
				case strings.Contains(w, "in total"):
					total++
				}
			}
			if (file == 1) != c.fileWarn || (total == 1) != c.total || file+total != len(got) {
				t.Fatalf("warnings %q, want file %v total %v", got, c.fileWarn, c.total)
			}
			if c.fileWarn && !strings.Contains(got[0], toolprofile.CanonicalOutput) {
				t.Fatalf("file warning names %q", got[0])
			}
		})
	}

	// The note: every file, private files summed, the total.
	home, root := t.TempDir(), t.TempDir()
	testenv.SetHome(t, home)
	write(t, filepath.Join(home, ".claude", "CLAUDE.md"), 7)
	priv := filepath.Join(home, "p.md")
	write(t, priv, 5)
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(pointer), 0o600); err != nil {
		t.Fatal(err)
	}
	local := "@" + priv + "\n"
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.local.md"), []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	want := "context: launch set for claude_code: ~/.claude/CLAUDE.md 7 (not edited by lore) + CLAUDE.md 15 + .lore/LORE.md 100 + CLAUDE.local.md " +
		strconv.Itoa(len(local)) + " + 1 private store files 5 = " + strconv.Itoa(7+15+100+len(local)+5) + " bytes"
	if got := launchSet(root, claude, 100); got != want {
		t.Fatalf("launch set\n got %q\nwant %q", got, want)
	}
}

// TestBudgetLimitNote (CONTEXT_BUDGET 6.1, FA-CTX-8, later:454): raising a
// budget is said out loud above 80% of the tool's documented per-file
// limit (claude_code 40,000, codex 32,768) and never for a profile with
// no limit; the policy write that carries it always confirms.
func TestBudgetLimitNote(t *testing.T) {
	cases := []struct {
		tool string
		n    int
		warn bool
	}{
		{toolprofile.NameClaudeCode, 32000, false},
		{toolprofile.NameClaudeCode, 32001, true},
		{toolprofile.NameClaudeCode, claudeStartFileMax, true},
		{toolprofile.NameCodex, 26214, false},
		{toolprofile.NameCodex, 26215, true},
		{toolprofile.NameGeminiCLI, 1 << 20, false},
	}
	for _, c := range cases {
		note := builtin(t, c.tool).LimitNote(c.n)
		if (note != "") != c.warn || c.warn && !strings.Contains(note, c.tool) {
			t.Errorf("%s %d: note %q, want warn %v", c.tool, c.n, note, c.warn)
		}
	}
	// The doctor's per-file level is the profile's documented limit.
	if builtin(t, toolprofile.NameClaudeCode).Limit != claudeStartFileMax {
		t.Fatalf("claude_code limit %d, doctor level %d", builtin(t, toolprofile.NameClaudeCode).Limit, claudeStartFileMax)
	}
}

// TestSuggestTargets (CONTEXT_BUDGET 6.7, later:454): a tool whose session
// ran in this repo of this project within 30 days and has a profile that
// is not a target gets one line with the command keeping the current
// targets; a target tool, a session older than 30 days (by ended_at, else
// started_at), another project, another repo, a tool with no profile and
// the generic profile get none; Copilot surfaces share the copilot
// profile; two sessions of one tool give one line; lines in name order.
func TestSuggestTargets(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	reg, err := toolprofile.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	ses := func(tool, project, repo string, ended, started time.Time) wire.SessionRow {
		r := wire.SessionRow{ProjectID: project, StartedAt: started, EndedAt: ended}
		r.Tool, r.Repo = tool, repo
		return r
	}
	day := 24 * time.Hour
	recent := now.Add(-2 * day)
	old := now.Add(-31 * day)
	sessions := []wire.SessionRow{
		ses(toolprofile.NameCodex, "prj_a", "app", recent, old),
		ses(toolprofile.NameCodex, "prj_a", "app", now, now),             // same tool again
		ses(toolprofile.NameClaudeCode, "prj_a", "app", recent, recent),  // already a target
		ses(toolprofile.NameGeminiCLI, "prj_a", "app", old, old),         // too old
		ses(toolprofile.NameCursor, "prj_a", "app", time.Time{}, recent), // no ended_at: started_at counts
		ses("copilot_vscode", "prj_a", "app", recent, recent),
		ses("aider", "prj_a", "app", recent, recent),                          // no profile
		ses(toolprofile.NameGeneric, "prj_a", "app", recent, recent),          // generic never
		ses(toolprofile.NameGeminiCLI, "prj_b", "app", recent, recent),        // another project
		ses(toolprofile.NameGeminiCLI, "prj_a", "other", recent, recent),      // another repo
		ses(toolprofile.NameGeminiCLI, "prj_a", "app", now.Add(-30*day), old), // exactly 30 days: kept
	}
	targets := []contextTarget{{profile: builtin(t, toolprofile.NameClaudeCode), known: true}, {profile: toolprofile.Profile{Name: "docs/x.md"}, file: "docs/x.md"}}
	got := suggestTargets(sessions, "prj_a", "app", now, reg, targets)
	want := []string{toolprofile.NameCodex, toolprofile.NameCopilot, toolprofile.NameCursor, toolprofile.NameGeminiCLI}
	if len(got) != len(want) {
		t.Fatalf("suggestions %q, want tools %v", got, want)
	}
	for i, tool := range want {
		cmd := "lore policy set --key context.targets --value " + toolprofile.NameClaudeCode + "," + tool + " --yes"
		if !strings.HasPrefix(got[i], "context: "+tool+" sessions ran in this repo") || !strings.HasSuffix(got[i], cmd) {
			t.Fatalf("line %d %q, want tool %s and %q", i, got[i], tool, cmd)
		}
	}
	if got := suggestTargets(nil, "prj_a", "app", now, reg, targets); len(got) != 0 {
		t.Fatalf("no sessions: %q", got)
	}
}
