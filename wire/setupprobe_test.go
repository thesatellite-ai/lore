package wire_test

// A13 bugs 2, 4, 5 and 9 on the wire prober: store.write over a file://
// store, the this_folder repo checks, the tools and folders checks, and
// the details of passing checks.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/internal/testenv"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/tests/fakeexec"
	"github.com/thesatellite-ai/lore/wire"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// probeOne runs one request through the App's prober.
func probeOne(t *testing.T, pr opssetup.Prober, r opssetup.ProbeRequest) opssetup.ProbeResult {
	t.Helper()
	got := pr.Probe(context.Background(), []opssetup.ProbeRequest{r})
	if len(got) != 1 {
		t.Fatalf("%d results", len(got))
	}
	return got[0]
}

func check(id onboard.CheckID, l onboard.Level) onboard.Check { return onboard.Check{ID: id, Level: l} }

func TestSetupProberWriteAndDetails(t *testing.T) {
	ctx := context.Background()
	h := bridgeHarness(t)
	url := bareStore(t)
	pr, err := h.App.SetupProber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ans := onboard.Answers{onboard.QIDStoreURL: {Text: url}}
	w := probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckStoreWrite, onboard.LevelError), Answers: ans})
	if w.Status != onboard.StatusOK || w.Detail == "" {
		t.Fatalf("store.write = %+v, want ok with a detail", w)
	}
	r := probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckStoreRead, onboard.LevelError), Answers: ans})
	if r.Status != onboard.StatusOK || r.Detail == "" {
		t.Fatalf("store.read = %+v, want ok with a detail", r)
	}
}

// gitInit makes a git repo with no .lore/.
func gitInit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return dir
}

func TestSetupProberRepoChecks(t *testing.T) {
	ctx := context.Background()
	h := bridgeHarness(t)
	pr, err := h.App.SetupProber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	solo := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo}
	// what `lore init` leaves (the harness project may not have written it yet)
	meta := filepath.Join(h.Repo, ".lore", "data", "_meta.json")
	if _, err := os.Stat(meta); err != nil {
		if err := os.MkdirAll(filepath.Dir(meta), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(meta, []byte(`{"_v":2}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if r := probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckRepoInit, onboard.LevelWarn), Setup: solo, Target: h.Repo}); r.Status != onboard.StatusOK {
		t.Fatalf("repo.init in an initialised repo = %+v", r)
	}
	fresh := gitInit(t)
	r := probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckRepoInit, onboard.LevelWarn), Setup: solo, Target: fresh})
	if r.Status != onboard.StatusWarn || r.Vars[onboard.VarRepoRoot] != fresh {
		t.Fatalf("repo.init in a fresh repo = %+v, want warn", r)
	}
	if _, err := os.Stat(filepath.Join(fresh, ".lore")); err == nil {
		t.Fatal("probing a fresh repo wrote .lore/ in it")
	}
	// No tools chosen: init keeps its default (no variant, no --target).
	if r.Vars[onboard.VarFixVariant] != "" || r.Vars[onboard.VarTargets] != "" {
		t.Fatalf("repo.init with no tools = %+v, want the plain lore init fix", r)
	}
	// The setup chose tools: the fix wires exactly those with a pointer
	// file, in the setup's order; unknown names are dropped, never passed.
	tooled := solo
	tooled.Tools = []string{"claude_code", "nosuchtool", "cursor", "claude_code"}
	r = probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckRepoInit, onboard.LevelWarn), Setup: tooled, Target: fresh})
	if r.Vars[onboard.VarFixVariant] != string(onboard.FixInitTargets) || r.Vars[onboard.VarTargets] != "claude_code,cursor" {
		t.Fatalf("repo.init with tools = %+v, want --target claude_code,cursor", r.Vars)
	}
	if r := probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckRepoStores, onboard.LevelError), Setup: solo}); r.Status != onboard.StatusOK {
		t.Fatalf("repo.stores with none attached = %+v", r)
	}
	if r := probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckRepoOrg, onboard.LevelError), Setup: solo}); r.Status != onboard.StatusOK {
		t.Fatalf("repo.org for a solo setup = %+v", r)
	}
}

func TestSetupProberToolsAndFolders(t *testing.T) {
	ctx := context.Background()
	h := bridgeHarness(t)
	st, err := h.App.SetupStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	for _, d := range []string{"a/b", "c"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.PutSetup(ctx, onboard.SetupEntry{Name: "acme-hub", Kind: onboard.KindSolo, Folders: []string{"~/a/b/**"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutSetup(ctx, onboard.SetupEntry{Name: "client", Kind: onboard.KindSolo, Folders: []string{"~/c/**"}}); err != nil {
		t.Fatal(err)
	}
	pr, err := h.App.SetupProber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	me := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Tools: []string{"claude_code"}}
	folders := func(globs ...string) onboard.Answers {
		return onboard.Answers{onboard.QIDFolders: {IsList: true, List: globs}}
	}
	sh := probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckFoldersShadow, onboard.LevelWarn), Setup: me, Answers: folders("~/a/**")})
	if sh.Status != onboard.StatusWarn || sh.Vars[onboard.VarOther] != "acme-hub" {
		t.Fatalf("folders.shadow = %+v, want warn naming acme-hub", sh)
	}
	ov := probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckFoldersOverlap, onboard.LevelError), Setup: me, Answers: folders("~/c/**")})
	if ov.Status != onboard.StatusFail || ov.Vars[onboard.VarOther] != "client" {
		t.Fatalf("folders.overlap = %+v, want fail naming client", ov)
	}
	if r := probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckFoldersOverlap, onboard.LevelError), Setup: me, Answers: folders("~/a/x/**")}); r.Status != onboard.StatusOK || r.Detail == "" {
		t.Fatalf("folders.overlap with no tie = %+v", r)
	}
	// the step form (no target) and doctor's form (target = the tool) agree
	for _, target := range []string{"", "claude_code"} {
		r := probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckToolsSkillInstalled, onboard.LevelWarn), Setup: me, Target: target})
		if r.Status != onboard.StatusWarn || r.Vars[onboard.VarTool] != "claude_code" {
			t.Fatalf("tools.skill_installed target %q = %+v, want warn for claude_code", target, r)
		}
	}
	if r := probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckToolsSkillInstalled, onboard.LevelWarn), Setup: onboard.SetupEntry{Name: "x", Kind: onboard.KindSolo}}); r.Status != onboard.StatusSkip {
		t.Fatalf("tools check with no tools = %+v, want skip", r)
	}
}

// toolsHarness is bridgeHarness with this binary's version set and PATH
// holding only fakeLore's folder (when not ""), so the PATH-lore lookup
// sees exactly what a test puts there.
func toolsHarness(t *testing.T, version, pathLoreOut string) *opstest.H {
	t.Helper()
	home := t.TempDir()
	testenv.SetHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	bin := t.TempDir()
	if pathLoreOut != "" {
		script := "#!/bin/sh\nprintf '%s\\n' '" + pathLoreOut + "'\n"
		fakeexec.Write(t, filepath.Join(bin, "lore"), script, 0o755)
	}
	gitDir := ""
	if g, err := exec.LookPath("git"); err == nil {
		gitDir = filepath.Dir(g)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+gitDir)
	return opstest.New(t, func(o *wire.Options) { o.Version = version })
}

// seedLoreMeta makes root look initialised (what `lore init` leaves).
func seedLoreMeta(t *testing.T, root string) {
	t.Helper()
	meta := filepath.Join(root, ".lore", "data", "_meta.json")
	if err := os.MkdirAll(filepath.Dir(meta), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meta, []byte(`{"_v":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestSetupProberSkillPickedTools: a tool check covers only the tools the
// Tools step picked (answers over the saved setup), and a Target narrows it
// to that one tool (the setup op probes one request per picked tool).
func TestSetupProberSkillPickedTools(t *testing.T) {
	ctx := context.Background()
	h := toolsHarness(t, "0.1.18", "")
	pr, err := h.App.SetupProber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	saved := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Tools: []string{"claude_code", "codex", "cursor", "gemini_cli", "copilot"}}
	picked := onboard.Answers{onboard.QIDTools: {IsList: true, List: []string{"codex"}}}
	installed := check(onboard.CheckToolsSkillInstalled, onboard.LevelWarn)
	cases := []struct {
		name    string
		answers onboard.Answers
		target  string
		want    string
		notIn   []string
	}{
		{"answers win over the saved tools", picked, "", "codex", []string{"claude_code", "cursor", "gemini", "copilot"}},
		{"target is one tool", nil, "claude_code", "claude_code", []string{"codex", "cursor"}},
		{"target with a repo root", nil, "codex " + h.Repo, "codex", []string{"claude_code"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := probeOne(t, pr, opssetup.ProbeRequest{Check: installed, Setup: saved, Answers: c.answers, Target: c.target})
			if r.Status != onboard.StatusWarn || r.Vars[onboard.VarTool] != c.want {
				t.Fatalf("result = %+v, want warn for %s", r, c.want)
			}
			for _, other := range c.notIn {
				if strings.Contains(r.Detail, other) {
					t.Errorf("detail names %s, which this row does not cover: %q", other, r.Detail)
				}
			}
		})
	}
	// An empty pick is a choice: no tools, skip (never the saved list).
	none := onboard.Answers{onboard.QIDTools: {IsList: true}}
	if r := probeOne(t, pr, opssetup.ProbeRequest{Check: installed, Setup: saved, Answers: none}); r.Status != onboard.StatusSkip {
		t.Fatalf("no tools picked = %+v, want skip", r)
	}
}

// TestSetupProberPointerFileTools: cursor, gemini_cli and copilot have no
// skill folder; tools.skill_installed checks their pointer file instead and
// never warns about a skill; skill_version / skill_modified skip them.
func TestSetupProberPointerFileTools(t *testing.T) {
	ctx := context.Background()
	h := toolsHarness(t, "0.1.18", "")
	seedLoreMeta(t, h.Repo)
	pr, err := h.App.SetupProber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	me := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Tools: []string{"cursor", "gemini_cli", "copilot"}}
	fresh := gitInit(t) // a repo without .lore/
	files := map[string]string{"cursor": ".cursor/rules/lore.mdc", "gemini_cli": "GEMINI.md", "copilot": ".github/copilot-instructions.md"}
	for tool, file := range files {
		t.Run(tool, func(t *testing.T) {
			probe := func(id onboard.CheckID, target string) opssetup.ProbeResult {
				return probeOne(t, pr, opssetup.ProbeRequest{Check: check(id, onboard.LevelWarn), Setup: me, Target: target})
			}
			inRepo := tool + " " + h.Repo
			// A pointer-file tool needs no skill: tools.skill_installed skips it
			// and names the file, whether or not the file exists, so a missing
			// pointer file is reported once, by tools.pointer_file.
			wantSkip := func(when string) {
				t.Helper()
				r := probe(onboard.CheckToolsSkillInstalled, inRepo)
				if r.Status != onboard.StatusSkip || !strings.Contains(r.Detail, "needs no skill") || !strings.Contains(r.Detail, file) {
					t.Fatalf("%s, %s = %+v, want a needs-no-skill skip naming %s", file, when, r, file)
				}
			}
			wantSkip("missing")
			// The one place a missing pointer file shows: tools.pointer_file.
			if r := probe(onboard.CheckToolsPointerFile, inRepo); r.Status == onboard.StatusOK || !strings.Contains(r.Detail, file) {
				t.Fatalf("tools.pointer_file with %s missing = %+v, want a failing row naming it", file, r)
			}
			p := filepath.Join(h.Repo, filepath.FromSlash(file))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("@.lore/LORE.md\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			wantSkip("present")
			if r := probe(onboard.CheckToolsPointerFile, inRepo); r.Status != onboard.StatusOK {
				t.Fatalf("tools.pointer_file with %s present = %+v, want ok", file, r)
			}
			if r := probe(onboard.CheckToolsSkillInstalled, tool+" "+fresh); r.Status != onboard.StatusSkip {
				t.Fatalf("repo without .lore/ = %+v, want skip", r)
			}
			for _, id := range []onboard.CheckID{onboard.CheckToolsSkillVersion, onboard.CheckToolsSkillModified} {
				if r := probe(id, inRepo); r.Status != onboard.StatusSkip {
					t.Fatalf("%s for %s = %+v, want skip", id, tool, r)
				}
			}
		})
	}
}

// TestSetupProberPathLoreOlder: a release lore on PATH older than this
// binary turns the install rows into info ("upgrade lore first"); none,
// the same version, an unreadable one, or a dev build of ours do not.
func TestSetupProberPathLoreOlder(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	cases := []struct {
		name, binary, pathOut string
		info                  bool
	}{
		{"owner: 0.1.17 installed, 0.1.18-dev here", "0.1.18-dev", "lore 0.1.17", true},
		{"older release", "0.1.18", "lore 0.1.17", true},
		{"same release", "0.1.18", "lore 0.1.18", false},
		{"no lore on PATH", "0.1.18-dev", "", false},
		{"unreadable output", "0.1.18-dev", "something else", false},
		{"unstamped dev binary", "dev", "lore 0.1.17", false},
		{"newer on PATH", "0.1.18-dev", "lore 0.1.19", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			h := toolsHarness(t, c.binary, c.pathOut)
			pr, err := h.App.SetupProber(ctx)
			if err != nil {
				t.Fatal(err)
			}
			me := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Tools: []string{"claude_code"}}
			r := probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckToolsSkillInstalled, onboard.LevelWarn), Setup: me, Target: "claude_code"})
			if r.Status != onboard.StatusWarn {
				t.Fatalf("result = %+v, want warn (not installed)", r)
			}
			gotInfo := r.Vars["level"] == "info"
			if gotInfo != c.info {
				t.Fatalf("info = %v, want %v: %+v", gotInfo, c.info, r)
			}
			if c.info && (r.Vars[onboard.VarFixVariant] != "after_upgrade" || !strings.Contains(r.Detail, "Upgrade lore first") || !strings.Contains(r.Detail, "0.1.17")) {
				t.Fatalf("info row = %+v", r)
			}
		})
	}
}

// TestSetupProberPointerFileGenerated (devco J-17): a CLAUDE.md another
// tool generates (its banner says so) failed tools.pointer_file with the
// fix "lore render --add-target claude_code", which render then skipped,
// so doctor stayed degraded forever. The row now names the banner and
// takes the generated fix, which names the generator's source and runs
// nothing.
func TestSetupProberPointerFileGenerated(t *testing.T) {
	ctx := context.Background()
	h := toolsHarness(t, "0.1.18", "")
	seedLoreMeta(t, h.Repo)
	pr, err := h.App.SetupProber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	banner := "<!-- GENERATED FILE — do not edit directly; run make agents -->"
	if err := os.WriteFile(filepath.Join(h.Repo, "CLAUDE.md"), []byte(banner+"\n# Shop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	me := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Tools: []string{"claude_code"}}
	r := probeOne(t, pr, opssetup.ProbeRequest{Check: check(onboard.CheckToolsPointerFile, onboard.LevelWarn), Setup: me, Target: "claude_code " + h.Repo})
	if r.Status == onboard.StatusOK || !strings.Contains(r.Detail, "another tool generates it") || r.Vars[onboard.VarFixVariant] != string(onboard.FixGenerated) {
		t.Fatalf("generated CLAUDE.md = %+v, want a failing row with the generated fix", r)
	}
	fix := onboard.FixFor(onboard.CheckToolsPointerFile, onboard.StepTools, onboard.FixGenerated)
	if fix.Runnable || len(fix.Command) != 0 || strings.Contains(fix.Text, "--add-target") || !strings.Contains(fix.Text, "the source that generates the file") {
		t.Fatalf("generated fix = %+v, want the generator's source and no command", fix)
	}
}
