package probe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/secretmgr"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// LookPath returns the program's absolute path and whether it is on PATH.
func (p *Prober) LookPath(name string) (string, bool) {
	exe, err := p.o.LookPath(name)
	if err != nil {
		return "", false
	}
	return exe, true
}

// toolHint is how one coding tool shows itself on a machine (ONB 4.7:
// "the tool's binary on PATH and its home folder").
type toolHint struct {
	tool string   // a toolprofile built-in name
	bins []string // programs on PATH
	dirs []string // home-relative folders or globs
}

// toolHints covers each built-in profile with something to detect;
// generic has no program or folder (it is the fallback, never "detected").
var toolHints = []toolHint{
	{tool: toolprofile.NameClaudeCode, bins: []string{"claude"}, dirs: []string{".claude"}},
	{tool: toolprofile.NameCodex, bins: []string{"codex"}, dirs: []string{".codex"}},
	{tool: toolprofile.NameCursor, bins: []string{"cursor", "cursor-agent"}, dirs: []string{".cursor"}},
	{tool: toolprofile.NameGeminiCLI, bins: []string{"gemini"}, dirs: []string{".gemini"}},
	// Copilot: the VS Code extension folder (desktop and remote server) or
	// the Copilot CLI and its folder.
	{tool: toolprofile.NameCopilot, bins: []string{"copilot"}, dirs: []string{".vscode/extensions/github.copilot-*", ".vscode-server/extensions/github.copilot-*", ".copilot"}},
}

// ToolDetection is one coding tool's presence (ONB 4.7 tools prefill).
type ToolDetection struct {
	Tool     string `json:"tool"` // toolprofile name
	Detected bool   `json:"detected"`
	// Binary is the program's path when on PATH; HomeDir the first folder found.
	Binary  string `json:"binary,omitempty"`
	HomeDir string `json:"home_dir,omitempty"`
}

// DetectTools reports each known coding tool (program on PATH or home
// folder present). Reads only; runs nothing.
func (p *Prober) DetectTools(ctx context.Context) []ToolDetection {
	out := make([]ToolDetection, 0, len(toolHints))
	for _, h := range toolHints {
		if ctx.Err() != nil {
			break
		}
		d := ToolDetection{Tool: h.tool}
		for _, b := range h.bins {
			if exe, ok := p.LookPath(b); ok {
				d.Binary = exe
				break
			}
		}
		for _, rel := range h.dirs {
			matches, _ := filepath.Glob(filepath.Join(p.o.Home, rel))
			for _, m := range matches {
				if st, err := os.Stat(m); err == nil && st.IsDir() {
					d.HomeDir = m
					break
				}
			}
			if d.HomeDir != "" {
				break
			}
		}
		d.Detected = d.Binary != "" || d.HomeDir != ""
		out = append(out, d)
	}
	return out
}

// secretsHint is one secrets manager's program and its harmless status
// call (ONB 4.8: a version print, never a read of a secret, never op signin).
type secretsHint struct {
	manager string // secretmgr scheme
	bin     string
	args    []string
}

// secretsHints covers every CLI manager of adapters/secretmgr. env has no
// program (it is always available) and is not listed.
var secretsHints = []secretsHint{
	{manager: secretmgr.Gopass, bin: "gopass", args: []string{"version"}},
	{manager: secretmgr.OnePass, bin: "op", args: []string{"--version"}},
	{manager: secretmgr.Bitwarden, bin: "bw", args: []string{"--version"}},
	{manager: secretmgr.Vault, bin: "vault", args: []string{"version"}},
	{manager: secretmgr.AWS, bin: "aws", args: []string{"--version"}},
	{manager: secretmgr.GCP, bin: "gcloud", args: []string{"--version"}},
	{manager: secretmgr.Azure, bin: "az", args: []string{"version"}},
}

// SecretsDetection is one secrets manager's presence.
type SecretsDetection struct {
	Outcome
	Manager string `json:"manager"` // secretmgr scheme
	Program string `json:"program"`
	Path    string `json:"path,omitempty"`
	// Answers: the version call exited 0 (Class ok). Version is its first line.
	Answers bool   `json:"answers"`
	Version string `json:"version,omitempty"`
}

// DetectSecrets reports each CLI secrets manager: on PATH, and answering
// its version call. Classes: ok, tool_missing, timeout, failed.
func (p *Prober) DetectSecrets(ctx context.Context) []SecretsDetection {
	// In parallel (the limiter bounds them): one after another, the slow
	// version calls (gcloud, az, aws) added up to many seconds.
	out := make([]SecretsDetection, len(secretsHints))
	var wg sync.WaitGroup
	for i, h := range secretsHints {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = p.detectSecret(ctx, h)
		}()
	}
	wg.Wait()
	return out
}

func (p *Prober) detectSecret(ctx context.Context, h secretsHint) SecretsDetection {
	start := time.Now()
	d := SecretsDetection{Manager: h.manager, Program: h.bin}
	exe, ok := p.LookPath(h.bin)
	if !ok {
		d.Outcome = Outcome{Class: ClassToolMissing, Detail: h.bin + " not found on PATH"}
		return d
	}
	d.Path = exe
	cctx, done, ok := p.slot(ctx, p.o.VersionTimeout)
	defer done()
	if !ok {
		d.Outcome = timedOut(start)
		return d
	}
	r := p.run(cctx, cmdSpec{bin: h.bin, args: h.args, env: []string{envLCAll + "=C", envNoColor + "=1"}})
	if !r.ok() {
		d.Outcome = failure(r, h.bin, func(string) Class { return ClassFailed })
		d.Detail = h.bin + " did not answer its version call (its message is not shown)"
		if r.timedOut {
			d.Detail = h.bin + " timed out on its version call"
		}
		d.Took = time.Since(start)
		return d
	}
	first, _, _ := strings.Cut(strings.TrimSpace(r.stdout+"\n"+r.stderr), "\n")
	d.Answers, d.Version = true, Sanitize(first)
	d.Outcome = Outcome{Class: ClassOK, Took: time.Since(start)}
	return d
}
