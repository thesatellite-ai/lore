package main

// Use-case tests for the skill compiler (`lore skill compile`, the Local
// operation skill.compile) and the TUI's tables screen. The skill compiler
// calls a model through llmcall.Auto(), which — with ANTHROPIC_API_KEY unset
// and no "ollama:" model prefix — shells out to a hosted model's
// command-line tool found on PATH; the end-to-end test puts a stand-in
// script there, so the real command runs with canned model output and no
// network. The bench tests are gone with `lore bench` (dropped in 0.1.18,
// p3/dropped.md).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	enttuirt "github.com/khanakia/entx/enttui/runtime"
)

func TestUC_BenchTUITablesHelpers(t *testing.T) {
	t.Parallel()
	if tablesSortField("rows") != "count" || tablesSortField("table") != "name" || tablesSortField("") != "name" {
		t.Fatal("tablesSortField")
	}
	if tablesDir(enttuirt.Desc) != "desc" || tablesDir(enttuirt.Asc) != "asc" {
		t.Fatal("tablesDir")
	}
	// Registration validates the spec (Kind, Fetch) and panics on a bad
	// one; the Fetch closure itself is private to the runtime.
	app := enttuirt.New()
	registerTablesScreen(app, filepath.Join(t.TempDir(), "lore.db"))
}

// fakeClaude installs a stand-in model command-line tool that answers with script (a POSIX sh body
// that may read the prompt on stdin) and returns the env for running lore
// against it.
func fakeClaude(t *testing.T, script string) []string {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\n" + script + "\n"
	writeExecutable(t, filepath.Join(dir, "claude"), body)
	return []string{
		"PATH=" + dir + string(os.PathListSeparator) + filepath.Dir(e2eBin) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"ANTHROPIC_API_KEY=", // empty = unset for llmcall's router
	}
}

func TestUC_BenchSkillCompileEndToEnd(t *testing.T) {
	w := newWorld(t)
	a := w.newClone("alice")
	src := filepath.Join(a.dir, "skill")
	if err := os.MkdirAll(src, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte(strings.Repeat("long skill text ", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args")
	env := fakeClaude(t, `echo "$@" > `+argsFile+`; grep -q "COMPRESSION RULES" || exit 3; printf 'Here is the compressed version:\n---\nname: lore\n---\nmini body\n'`)

	out := filepath.Join(a.dir, "mini.md")
	r := w.run(a.dir, env, e2eBin, "skill", "compile", "--source-dir="+src, "--output="+out, "--model=claude-test", "--json")
	if r.code != 0 {
		t.Fatalf("compile: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if got := readFileE2E(t, out); got != "---\nname: lore\n---\nmini body\n" {
		t.Fatalf("written output must have the preamble stripped: %q", got)
	}
	if args := readFileE2E(t, argsFile); !strings.Contains(args, "--model claude-test") {
		t.Fatalf("model not passed to the provider: %q", args)
	}
	// skill.compile answers the plain operation object (P3-SKILL-1).
	var env2 struct {
		OutputBytes int     `json:"output_bytes"`
		SourceBytes int     `json:"source_bytes"`
		Ratio       float64 `json:"compression_x"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &env2); err != nil || env2.SourceBytes != 1600 || env2.OutputBytes != len("---\nname: lore\n---\nmini body\n") || env2.Ratio <= 1 {
		t.Fatalf("json: %+v %v", env2, err)
	}

	// --dry-run prints and writes nothing; the default output is a DRAFT path.
	r = w.run(a.dir, env, e2eBin, "skill", "compile", "--source-dir="+src, "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, "mini body") {
		t.Fatalf("dry run: %s %s", r.stdout, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(src, "SKILL-mini-draft.md")); !os.IsNotExist(err) {
		t.Fatal("--dry-run wrote a file")
	}
	r = w.run(a.dir, env, e2eBin, "skill", "compile", "--source-dir="+src)
	if r.code != 0 || readFileE2E(t, filepath.Join(src, "SKILL-mini-draft.md")) != "---\nname: lore\n---\nmini body\n" {
		t.Fatalf("default output must be the draft path: %s", r.stderr)
	}

	for name, args := range map[string][]string{
		"unsupported target": {"skill", "compile", "--source-dir=" + src, "--target=full"},
		"no sources":         {"skill", "compile", "--source-dir=" + t.TempDir()},
	} {
		if r := w.run(a.dir, env, e2eBin, args...); r.code == 0 {
			t.Fatalf("%s: must fail", name)
		}
	}
	failing := fakeClaude(t, `cat >/dev/null; exit 1`)
	if r := w.run(a.dir, failing, e2eBin, "skill", "compile", "--source-dir="+src, "--output="+out); r.code == 0 {
		t.Fatal("a failing model must fail the compile")
	}
}

// runTUI builds the whole TUI (project scope, every generated screen, the
// tables screen) and then opens /dev/tty. Without a controlling terminal
// that open fails, so the run returns an error instead of taking over the
// screen; everything before it is exercised. With a terminal attached the
// TUI would open for real, so the test only runs headless.
func TestUC_BenchRunTUIHeadless(t *testing.T) {
	if tty, err := os.Open("/dev/tty"); err == nil {
		_ = tty.Close()
		t.Skip("a controlling terminal is attached; `lore tui` would open for real")
	}
	w := newWorld(t)
	a := w.newClone("alice")
	a.lore("init", "--non-interactive", "--name=app")
	r := a.loreAny("tui", "--kind=task", "--view=list")
	if r.code == 0 || !strings.Contains(r.stderr, "tty") {
		t.Fatalf("headless tui must fail on the terminal, after building every screen: %d %s", r.code, r.stderr)
	}
	if strings.Contains(r.stderr, "panic") {
		t.Fatalf("building the screens panicked: %s", r.stderr)
	}
	// Outside a project it fails before touching the terminal.
	r = w.run(t.TempDir(), nil, e2eBin, "tui")
	if r.code == 0 || strings.Contains(r.stderr, "tty") {
		t.Fatalf("outside a project: %d %s", r.code, r.stderr)
	}
}
