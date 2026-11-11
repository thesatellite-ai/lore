//go:build userscen

package userscen

// campaign_render_help_test.go — the help and the shell completion of the
// render area as a person meets them on a new machine:
//   - every command of the area (and each completion shell) prints an
//     Examples block with at least one runnable `lore …` line;
//   - `lore completion <shell>` prints a script the shell loads: bash and
//     zsh source it and register lore; fish and PowerShell are parsed when
//     the shell is installed, and always carry their registration line;
//   - the scripts work outside any repo (completion needs no project), an
//     unknown shell is E_INVALID_INPUT;
//   - the completion engine answers the area's own commands and flags.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// campAreaCommands are the render area's commands (inventory.md).
var campAreaCommands = [][]string{
	{"check", "leaks"}, {"check", "pr-body"}, {"completion"},
	{"completion", "bash"}, {"completion", "zsh"}, {"completion", "fish"}, {"completion", "powershell"},
	{"context", "fetch"}, {"init"}, {"policy", "set"}, {"policy", "show"}, {"policy", "unset"}, {"render", "run"},
}

func TestCampaignRender_HelpHasExamples(t *testing.T) {
	c := renderSolo(t)
	for _, path := range campAreaCommands {
		t.Run(strings.Join(path, "_"), func(t *testing.T) {
			r := c.MustLore(append(path, "--help")...)
			i := strings.Index(r.Stdout, "\nExamples:\n")
			if i < 0 {
				t.Fatalf("lore %s --help has an Examples block:\n%s", strings.Join(path, " "), r.Stdout)
			}
			block := r.Stdout[i:]
			if j := strings.Index(block[1:], "\n\n"); j >= 0 {
				block = block[:j+1]
			}
			if !strings.Contains(block, "lore "+path[0]) {
				t.Errorf("lore %s --help: an example runs lore %s:\n%s", strings.Join(path, " "), path[0], block)
			}
		})
	}
}

func TestCampaignRender_CompletionScripts(t *testing.T) {
	c := renderSolo(t)
	// Outside any repo: completion needs no project.
	outside := filepath.Join(c.A.Home, "no-repo")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	marks := map[string]string{
		"bash":       "complete -o default -F __start_lore lore",
		"zsh":        "compdef _lore lore",
		"fish":       "complete -c lore",
		"powershell": "Register-ArgumentCompleter -CommandName 'lore'",
	}
	scripts := map[string]string{}
	for sh, mark := range marks {
		r := c.A.Lore(outside, "completion", sh).OK(t)
		if !strings.Contains(r.Stdout, mark) {
			t.Errorf("completion %s registers lore (%q):\n%.400s", sh, mark, r.Stdout)
		}
		if r.Stderr != "" {
			t.Errorf("completion %s writes nothing on stderr (it is piped into the shell): %s", sh, r.Stderr)
		}
		f := filepath.Join(t.TempDir(), "lore."+sh)
		if err := os.WriteFile(f, []byte(r.Stdout), 0o600); err != nil {
			t.Fatal(err)
		}
		scripts[sh] = f
	}
	// The shell loads what lore printed.
	loads := []struct {
		shell string
		args  []string
		want  string
	}{
		{"bash", []string{"--norc", "--noprofile", "-c", "source " + filepath.ToSlash(scripts["bash"]) + " && complete -p lore"}, "__start_lore"},
		{"zsh", []string{"-f", "-c", "autoload -U compinit && compinit -u -D && source " + filepath.ToSlash(scripts["zsh"]) + " && print -r -- ${_comps[lore]}"}, "_lore"},
		{"fish", []string{"--no-config", "-c", "source " + filepath.ToSlash(scripts["fish"]) + "; and complete -c lore | head -n 1"}, "lore"},
		{"pwsh", []string{"-NoProfile", "-NonInteractive", "-Command", ". " + scripts["powershell"] + "; 'loaded'"}, "loaded"},
	}
	for _, l := range loads {
		bin, err := exec.LookPath(l.shell)
		if err != nil {
			t.Logf("%s is not installed here: its script is checked by its registration line only", l.shell)
			continue
		}
		cmd := exec.Command(bin, l.args...)
		cmd.Env = append(os.Environ(), "HOME="+c.A.Home, "ZDOTDIR="+c.A.Home)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), l.want) {
			t.Errorf("%s loads lore's script: %v\n%s", l.shell, err, out)
		}
	}
	c.A.Lore(outside, "completion", "tcsh").Fails(t, campExitBadInput, "[E_INVALID_INPUT]", "unknown command")
	// The engine behind every script answers the area's commands and flags.
	for _, q := range []struct {
		args []string
		want string
	}{
		{[]string{"__complete", "pol"}, "policy"},
		{[]string{"__complete", "policy", ""}, "unset"},
		{[]string{"__complete", "render", "--ch"}, "--check"},
		{[]string{"__complete", "context", "--for-"}, "--for-file"},
		{[]string{"__complete", "check", "p"}, "pr-body"},
		{[]string{"__complete", "policy", "unset", "k", "--rea"}, "--reason"},
	} {
		r := c.MustLore(q.args...)
		if !strings.Contains(r.Stdout, q.want) {
			t.Errorf("lore %s completes %s: %s", strings.Join(q.args, " "), q.want, r.Stdout)
		}
	}
}
