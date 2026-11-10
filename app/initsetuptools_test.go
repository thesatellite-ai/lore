package app

// Owner decision (2026-11-07): plain `lore init` in a folder a setup
// applies to wires the pointer files of that setup's chosen tools (Claude
// Code → CLAUDE.md) and says which setup chose them; with no setup, or a
// setup that chose no tool with a pointer file, init keeps its default
// (the tool files found in the repo, else AGENTS.md); --target always wins.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSetup writes a config.yaml with one solo setup named name covering
// folder, with the given tools.
func writeSetup(t *testing.T, w *cliWorld, name, folder string, tools ...string) {
	t.Helper()
	dir := filepath.Join(w.env["HOME"], ".lore")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "setups:\n  - name: " + name + "\n    kind: solo\n    folders:\n      - " + folder + "/**\n"
	if len(tools) > 0 {
		cfg += "    tools:\n"
		for _, tl := range tools {
			cfg += "      - " + tl + "\n"
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestInitWiresTheApplyingSetupsTools(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		tools     []string // nil: no config.yaml at all
		args      []string
		want      []string
		notWant   []string
		saysSetup bool
	}{
		{name: "setup with claude_code", tools: []string{"claude_code"}, want: []string{"CLAUDE.md"}, notWant: []string{"AGENTS.md"}, saysSetup: true},
		{name: "no setup applies", want: []string{"AGENTS.md"}, notWant: []string{"CLAUDE.md"}},
		{name: "--target wins over the setup", tools: []string{"claude_code"}, args: []string{"--target", "codex"}, want: []string{"AGENTS.md"}, notWant: []string{"CLAUDE.md"}},
		{name: "setup with unknown tools only", tools: []string{"nosuchtool"}, want: []string{"AGENTS.md"}, notWant: []string{"CLAUDE.md"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newCLIWorld(t).person().unscoped()
			w.git(t, w.root, "init", "-q", "-b", "main")
			if c.tools != nil {
				writeSetup(t, w, "test1", w.root, c.tools...)
			}
			out := w.mustLore(t, w.root, append([]string{"init"}, c.args...)...)
			for _, f := range c.want {
				if !exists(filepath.Join(w.root, f)) {
					t.Errorf("init did not write %s\n%s", f, out)
				}
			}
			for _, f := range c.notWant {
				if exists(filepath.Join(w.root, f)) {
					t.Errorf("init wrote %s\n%s", f, out)
				}
			}
			if says := strings.Contains(out, "tool files from setup test1: CLAUDE.md"); says != c.saysSetup {
				t.Errorf("output names the setup = %v, want %v\n%s", says, c.saysSetup, out)
			}
		})
	}
}
