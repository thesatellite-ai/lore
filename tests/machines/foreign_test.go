//go:build machines && !windows

package machines

// foreign_test.go — the foreign_* shapes (shape_foreign.go): files someone
// else wrote. Each test runs the commands a person runs through the real
// binary and checks the result, what is on disk afterwards (their text
// kept byte for byte outside lore's blocks, their hooks and settings
// keys, their config.yaml comments and unknown keys, modes) and doctor.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fgRead is the file at p ("" and a test error when it cannot be read).
func fgRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Errorf("read %s: %v", p, err)
	}
	return string(b)
}

// fgBlocks are lore's blocks in a tool file, each with the blank line
// lore puts after a block it places at the top.
var fgBlocks = []*regexp.Regexp{
	regexp.MustCompile(`(?s)<!-- lore:directive:start -->.*?<!-- lore:directive:end -->\n\n?`),
	regexp.MustCompile(`(?s)<!-- lore:pointer:start -->.*?<!-- lore:pointer:end -->\n\n?`),
	regexp.MustCompile(`(?s)<!-- lore:context:start -->.*?<!-- lore:context:end -->\n\n?`),
}

// fgOutside is content without lore's blocks: the person's text.
func fgOutside(content string) string {
	for _, re := range fgBlocks {
		content = re.ReplaceAllString(content, "")
	}
	return content
}

// fgRun runs args in dir and wants exit 0.
func fgRun(t *testing.T, m *Machine, dir string, args ...string) Result {
	t.Helper()
	r := m.Lore(t, dir, args...)
	if r.Code != 0 {
		t.Fatalf("%s", r)
	}
	return r
}

// fgJSON decodes a JSON file into a generic tree.
func fgJSON(t *testing.T, p string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(fgRead(t, p)), &v); err != nil {
		t.Errorf("%s is no longer JSON: %v", p, err)
	}
	return v
}

// fgHasHook: hooks[event] holds an entry whose hooks run cmd.
func fgHasHook(settings map[string]any, event, cmd string) bool {
	hooks, _ := settings["hooks"].(map[string]any)
	list, _ := hooks[event].([]any)
	for _, e := range list {
		em, _ := e.(map[string]any)
		inner, _ := em["hooks"].([]any)
		for _, h := range inner {
			if hm, _ := h.(map[string]any); hm["command"] == cmd {
				return true
			}
		}
	}
	return false
}

// fgMode wants p's permission bits to be want.
func fgMode(t *testing.T, step, p string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil {
		t.Errorf("[%s] %v", step, err)
		return
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("[%s] %s is %#o, want %#o", step, p, got, want)
	}
}

// TestForeignTools: lore init, render, directive install, the agent hooks
// and skill install in a repo full of other tools' hand-edited files, run
// twice. The person's text outside lore's blocks is kept byte for byte,
// a block whose end marker they deleted is named (never swallowed), their
// hooks and settings keys stay, the files lore never writes are untouched,
// and doctor is healthy afterwards.
func TestForeignTools(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeForeignTools, loreBin)
	repo := m.Repos[RepoForeign]
	at := func(rel string) string { return filepath.Join(repo, rel) }
	untouched := map[string]string{
		at(".cursor/rules/team.mdc"):               foreignCursorRule,
		at(".cursor/hooks.json"):                   foreignCursorHooks,
		filepath.Join(m.Home, ".claude/CLAUDE.md"): foreignHomeClaude,
	}
	outside := map[string]string{
		at("CLAUDE.md"):                       fgOutside(foreignClaudeTop + foreignClaudeBlock + foreignClaudeBottom),
		at("AGENTS.md"):                       foreignAgentsTop + foreignAgentsStart + foreignAgentsRest,
		at(".github/copilot-instructions.md"): foreignCopilot,
	}
	var before map[string]any
	if err := json.Unmarshal([]byte(foreignClaudeSetting), &before); err != nil {
		t.Fatal(err)
	}
	for pass := 1; pass <= 2; pass++ {
		step := "pass " + string(rune('0'+pass))
		if r := m.Lore(t, repo, "init"); !alreadyInit(r) {
			t.Fatalf("[%s] %s", step, r)
		}
		r := fgRun(t, m, repo, "render")
		// The AGENTS.md start marker without its end is named with its
		// line: the last start marker, below lore's own block.
		agents := fgRead(t, at("AGENTS.md"))
		line := strings.Count(agents[:strings.LastIndex(agents, foreignAgentsStart)], "\n") + 1
		if !strings.Contains(r.Stdout, at("AGENTS.md")+" line "+strconv.Itoa(line)+": <!-- lore:pointer:start --> is half of a lore block") {
			t.Errorf("[%s] render does not name the stray marker of AGENTS.md: %s", step, r)
		}
		if strings.Contains(r.Stdout, at("CLAUDE.md")+" line") {
			t.Errorf("[%s] render names a stray marker in CLAUDE.md, which has none: %s", step, r)
		}
		fgRun(t, m, repo, "directive", "install")
		fgRun(t, m, repo, "setup", "--agent-hooks", "--yes")
		fgRun(t, m, "", "skill", "install")
		for p, want := range untouched {
			if got := fgRead(t, p); got != want {
				t.Errorf("[%s] %s changed, lore never writes it:\n%s", step, rel(m.Home, p), got)
			}
		}
		for p, want := range outside {
			got := fgRead(t, p)
			if fgOutside(got) != want {
				t.Errorf("[%s] %s: the person's text outside lore's blocks changed\nwant %q\ngot  %q", step, rel(m.Home, p), want, fgOutside(got))
			}
			if !strings.Contains(got, "<!-- lore:pointer:end -->") || !strings.Contains(got, "<!-- lore:directive:end -->") {
				t.Errorf("[%s] %s lacks lore's pointer or directive block:\n%s", step, rel(m.Home, p), got)
			}
			fgMode(t, step, p, 0o644)
		}
		// The hand edit inside lore's old CLAUDE.md block is lore's to replace.
		if got := fgRead(t, at("CLAUDE.md")); strings.Contains(got, "@.lore/OLD.md") || strings.Count(got, "<!-- lore:pointer:start -->") != 1 {
			t.Errorf("[%s] CLAUDE.md does not hold exactly one current pointer block:\n%s", step, got)
		}
		after := fgJSON(t, at(".claude/settings.json"))
		for _, k := range []string{"model", "permissions", "customKey"} {
			if !reflect.DeepEqual(after[k], before[k]) {
				t.Errorf("[%s] .claude/settings.json %s changed: %v → %v", step, k, before[k], after[k])
			}
		}
		for ev, cmd := range map[string]string{"PreToolUse": "./scripts/guard.sh", "SessionStart": "echo hello"} {
			if !fgHasHook(after, ev, cmd) {
				t.Errorf("[%s] .claude/settings.json lost the person's %s hook %q", step, ev, cmd)
			}
		}
		if !fgHasHook(after, "SessionStart", "lore agent start --tool claude_code --hook-stdin") {
			t.Errorf("[%s] .claude/settings.json has no lore session hook", step)
		}
		local := fgJSON(t, at(".claude/settings.local.json"))
		if perms, _ := local["permissions"].(map[string]any); !reflect.DeepEqual(perms["deny"], []any{"Bash(rm:*)"}) {
			t.Errorf("[%s] settings.local.json lost the person's deny list: %v", step, local)
		}
		// Every hook list holds lore's hook once, however often it ran.
		hooks, _ := after["hooks"].(map[string]any)
		if list, _ := hooks["SessionStart"].([]any); len(list) != 2 {
			t.Errorf("[%s] SessionStart holds %d entries, want the person's and lore's: %v", step, len(list), list)
		}
		m.CheckModes(t, ShapeForeignTools+"/"+step)
	}
	// the shape's top-level identity: equals the setup's: the leftover is
	// flagged and its fix retires it (SOURCE_OF_TRUTH row 1)
	sotRetireTopIdentity(t, m, "", ForeignSetup)
	r := m.Lore(t, "", "doctor", "--all")
	if r.Code != 0 || !strings.Contains(r.Stdout, "0 warnings · 0 errors") {
		t.Errorf("doctor --all after the foreign tools: %s", r)
	}
	m.CheckMachine(t, ShapeForeignTools+"/after", Gone{})
}

// handComments are every comment of ForeignHandConfig.
var handComments = []string{"# my lore config — edited by hand", "# keep this header", "# me", "# a key lore does not know", "# the main one", "# side projects, 4-space indent", "# trailing comment"}

// TestForeignHandcfg: the commands that write config.yaml, run on the
// file the person wrote by hand. After every one: their comments, unknown
// keys and CRLF endings are all still there and where they wrote them
// (the header together, the trailing comment last), nothing nested is
// written in flow style, the file parses with only the unknown key as a
// warning, and its mode is unchanged.
func TestForeignHandcfg(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeForeignHandcfg, loreBin)
	cfg := filepath.Join(m.Home, ".lore", "config.yaml")
	if r := fgRun(t, m, "", "config", "get", "my_notes"); strings.TrimSpace(r.Stdout) != "keep me" {
		t.Errorf("config get my_notes: %s", r)
	}
	l := m.List(t, "")
	if len(l.Setups) != 2 || l.Active.Name != ForeignSetup {
		t.Errorf("setup list of the hand-written file: %+v", l)
	}
	flowNested := regexp.MustCompile(`(?m)^\s*[\w-]+: *[\[{].*[\[{]`)
	steps := [][]string{
		{"config", "set", "live.autopush", "false"},
		{"config", "unset", "live.autopush"},
		{"setup", "edit", ForeignSetup, "--note", "edited once"},
		{"setup", "edit", "side", "--folder", "~/side2/**"},
		{"setup", "apply", "extra", "--step", "kind", "--answers", csKindAnswers("extra")},
		{"setup", "remove", "side"},
		{"setup", "trash", "restore", "side"},
		{"setup", "remove", "extra"},
		{"setup", "trash", "purge", "extra", "--confirm"},
		{"setup", "edit", ForeignSetup, "--default"},
	}
	for _, args := range steps {
		step := strings.Join(args, " ")
		fgRun(t, m, "", args...)
		got := fgRead(t, cfg)
		for _, c := range handComments {
			if !strings.Contains(got, c) {
				t.Errorf("[%s] config.yaml lost the comment %q:\n%s", step, c, got)
			}
		}
		for _, k := range []string{"my_notes: keep me", `my_setup_note: "hand added"`} {
			if !strings.Contains(got, k) {
				t.Errorf("[%s] config.yaml lost the person's key %q:\n%s", step, k, got)
			}
		}
		if !strings.Contains(got, "# my lore config — edited by hand\r\n# keep this header\r\n") {
			t.Errorf("[%s] the header comment is no longer together:\n%q", step, got)
		}
		if !strings.HasSuffix(strings.TrimRight(got, "\r\n"), "\n# trailing comment") {
			t.Errorf("[%s] the trailing comment is no longer the file's last line at column 0:\n%s", step, got)
		}
		if n := strings.Count(got, "\n"); n != strings.Count(got, "\r\n") {
			t.Errorf("[%s] the CRLF file now mixes line endings:\n%q", step, got)
		}
		if loc := flowNested.FindString(strings.ReplaceAll(got, "\r", "")); loc != "" {
			t.Errorf("[%s] config.yaml holds a nested flow line %q", step, loc)
		}
		r := fgRun(t, m, "", "config", "check", "--json")
		if w := decode[checkOut](t, r).Warnings; len(w) != 1 || !strings.Contains(w[0], "my_notes") {
			t.Errorf("[%s] config check warnings %v, want only the unknown my_notes", step, w)
		}
		fgMode(t, step, cfg, 0o600)
	}
	// The setup restored from the trash took its own comment along and back.
	got := strings.ReplaceAll(fgRead(t, cfg), "\r", "")
	if !strings.Contains(got, "# side projects, 4-space indent\n  - name: side") {
		t.Errorf("side's comment is not above it again after the restore:\n%s", got)
	}
	if r := m.Lore(t, "", "doctor", "--all", "--json"); !slices.Contains(doctorExits, r.Code) {
		t.Errorf("doctor --all: %s", r)
	}
	m.CheckDoctor(t, ShapeForeignHandcfg+"/after")
	m.CheckModes(t, ShapeForeignHandcfg+"/after")
}

// TestForeignHandcfgTabs: a config.yaml indented with a tab is refused by
// every command that reads or writes it with the reason and the file, and
// is never rewritten.
func TestForeignHandcfgTabs(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeForeignHandcfg, loreBin)
	cfg := filepath.Join(m.Home, ".lore", "config.yaml")
	tabbed := "setups:\n\t- name: " + ForeignSetup + "\n\t  kind: solo\n"
	if err := os.WriteFile(cfg, []byte(tabbed), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"config", "check"},
		{"config", "set", "live.autopush", "false"},
		{"setup", "list"},
		{"setup", "edit", ForeignSetup, "--note", "x"},
		{"setup", "apply", "extra", "--step", "kind", "--answers", csKindAnswers("extra")},
	} {
		step := strings.Join(args, " ")
		r := m.Lore(t, "", args...)
		out := r.Stdout + r.Stderr
		// setup list is a read that shows the problem in place of the
		// setups (the page lists it the same way); every other command
		// refuses.
		refused := r.Code != 0 || (args[0] == "setup" && args[1] == "list" && strings.Contains(out, "config problem"))
		if !refused || !strings.Contains(out, "E_CONFIG_INVALID") || !strings.Contains(out, "config.yaml") || !strings.Contains(out, "indented with a tab") {
			t.Errorf("[%s] a tab-indented config.yaml is not refused with E_CONFIG_INVALID naming the file and the tab: %s", step, r)
		}
		if strings.Contains(out, "panic") || strings.Contains(out, "goroutine ") {
			t.Errorf("[%s] crashed: %s", step, r)
		}
		// Each line names the problem once (no "x: reason: reason").
		for _, ln := range strings.Split(out, "\n") {
			if n := strings.Count(ln, "indented with a tab"); n > 1 {
				t.Errorf("[%s] a line names the problem %d times: %s", step, n, ln)
			}
		}
		if got := fgRead(t, cfg); got != tabbed {
			t.Errorf("[%s] the refused file was rewritten:\n%q", step, got)
		}
	}
	m.CheckDoctor(t, ShapeForeignHandcfg+"/tabs")
	// doctor --all reports the broken file (exit 2) and names the tab.
	if r := m.Lore(t, "", "doctor", "--all"); r.Code != 2 || !strings.Contains(r.Stdout, "indented with a tab") {
		t.Errorf("doctor --all on a tab-indented config.yaml: %s", r)
	}
}

// TestForeignV0117: a v0.1.17 install's leftovers in ~/.lore and in repos.
// The import offers exactly the old identity files present; lore init in
// a repo with 0.1.17's hook blocks (in .git/hooks, and in a
// core.hooksPath folder) updates its own block in place, keeps the
// person's hook lines and adds no second block; the 0.1.17 pointer block
// in CLAUDE.md is recognised as lore's; after import doctor is healthy.
func TestForeignV0117(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeForeignV0117, loreBin)
	repo, hp := m.Repos[RepoForeign], m.Repos[RepoForeignHP]
	scan := fgRun(t, m, "", "setup", "import-scan", "--json")
	var sc struct {
		Found struct {
			LegacyFiles []string `json:"legacy_files"`
		} `json:"found"`
		Proposals []json.RawMessage `json:"proposals"`
	}
	if err := json.Unmarshal([]byte(scan.Stdout), &sc); err != nil {
		t.Fatalf("import-scan: %v\n%s", err, scan)
	}
	if want := []string{"~/.lore/identity.yaml", "~/.lore/identity.toml"}; !reflect.DeepEqual(sc.Found.LegacyFiles, want) || len(sc.Proposals) != 1 {
		t.Errorf("import-scan offers %v (%d proposals), want exactly %v and one setup", sc.Found.LegacyFiles, len(sc.Proposals), want)
	}
	for _, dir := range []string{repo, hp} {
		fgRun(t, m, dir, "init")
		fgRun(t, m, dir, "render")
	}
	claude := fgRead(t, filepath.Join(repo, "CLAUDE.md"))
	if claude != v0117ClaudeTop+v0117ClaudeBlock+v0117ClaudeBottom {
		t.Errorf("CLAUDE.md with 0.1.17's pointer block was rewritten:\n%s", claude)
	}
	for _, p := range []string{filepath.Join(repo, ".git/hooks/pre-commit"), filepath.Join(hp, HooksPathRel, "pre-commit")} {
		got := fgRead(t, p)
		if !strings.HasPrefix(got, "#!/bin/sh\n# the person's own pre-commit line\nmake lint\n") {
			t.Errorf("%s lost the person's lines:\n%s", p, got)
		}
		if strings.Count(got, "# >>> lore-sync >>>") != 1 || strings.Count(got, "# <<< lore-sync <<<") != 1 || !strings.Contains(got, "lore migrate --help") {
			t.Errorf("%s does not hold exactly one current lore-sync block:\n%s", p, got)
		}
		fgMode(t, "hooks", p, 0o755)
	}
	if got := fgRead(t, filepath.Join(repo, ".git/hooks/post-commit")); got != v0117PostCmt {
		t.Errorf("0.1.17's lore-sync:live block (the same bytes 0.1.18 writes) was rewritten:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(hp, ".git/hooks/pre-commit")); err == nil {
		t.Errorf("lore wrote .git/hooks/pre-commit although core.hooksPath points at %s", HooksPathRel)
	}
	// Import the old identity as the proposal says, then doctor is clean
	// and nothing old is offered again.
	var p struct {
		Setup json.RawMessage `json:"setup"`
	}
	if len(sc.Proposals) > 0 {
		if err := json.Unmarshal(sc.Proposals[0], &p); err != nil {
			t.Fatal(err)
		}
		fgRun(t, m, "", "setup", "import", "--setups", "["+string(p.Setup)+"]")
	}
	again := fgRun(t, m, "", "setup", "import-scan", "--json")
	if strings.Contains(again.Stdout, `"proposals":[{`) {
		t.Errorf("import-scan offers the imported setup again: %s", again.Stdout)
	}
	r := m.Lore(t, "", "doctor", "--all")
	if r.Code != 0 || !strings.Contains(r.Stdout, "0 errors") {
		t.Errorf("doctor --all after the import: %s", r)
	}
	m.CheckMachine(t, ShapeForeignV0117+"/after import", Gone{})
}
