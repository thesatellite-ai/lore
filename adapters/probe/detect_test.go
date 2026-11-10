package probe

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/secretmgr"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

func TestDetectTools(t *testing.T) {
	s := newSandbox(t)
	t.Setenv("PATH", s.bin) // only the fakes
	fakeBin(t, s.bin, "codex", "exit 0")
	for _, d := range []string{".claude", ".vscode/extensions/github.copilot-1.250.0"} {
		if err := os.MkdirAll(filepath.Join(s.home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// a file named like a tool folder is not the folder
	if err := os.WriteFile(filepath.Join(s.home, ".gemini"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := map[string]ToolDetection{}
	for _, d := range s.prober(Options{}).DetectTools(ctxT(t)) {
		got[d.Tool] = d
	}
	want := map[string]bool{toolprofile.NameClaudeCode: true, toolprofile.NameCodex: true, toolprofile.NameCopilot: true, toolprofile.NameCursor: false, toolprofile.NameGeminiCLI: false}
	for tool, det := range want {
		if got[tool].Detected != det {
			t.Errorf("%s: %+v", tool, got[tool])
		}
	}
	if got[toolprofile.NameCodex].Binary != filepath.Join(s.bin, "codex") || got[toolprofile.NameCodex].HomeDir != "" {
		t.Errorf("codex: %+v", got[toolprofile.NameCodex])
	}
	if !strings.HasSuffix(got[toolprofile.NameCopilot].HomeDir, "github.copilot-1.250.0") {
		t.Errorf("copilot: %+v", got[toolprofile.NameCopilot])
	}
}

func TestDetectSecrets(t *testing.T) {
	s := newSandbox(t)
	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv("FAKE_LOG", log)
	t.Setenv("PATH", s.bin)
	fakeBin(t, s.bin, "gopass", `echo "gopass $*" >> "$FAKE_LOG"; echo "gopass 1.15.11 go1.23 linux amd64"`)
	fakeBin(t, s.bin, "op", `echo "op $*" >> "$FAKE_LOG"; echo "2.30.0"`)
	fakeBin(t, s.bin, "bw", `echo "bw $*" >> "$FAKE_LOG"; exit 3`)
	got := map[string]SecretsDetection{}
	for _, d := range s.prober(Options{}).DetectSecrets(ctxT(t)) {
		got[d.Manager] = d
	}
	if d := got[secretmgr.Gopass]; d.Class != ClassOK || !d.Answers || !strings.HasPrefix(d.Version, "gopass 1.15") {
		t.Errorf("gopass: %+v", d)
	}
	if d := got[secretmgr.OnePass]; !d.Answers || d.Version != "2.30.0" {
		t.Errorf("op: %+v", d)
	}
	if d := got[secretmgr.Bitwarden]; d.Class != ClassFailed || d.Answers || d.Path == "" {
		t.Errorf("bw: %+v", d)
	}
	if d := got[secretmgr.Vault]; d.Class != ClassToolMissing {
		t.Errorf("vault: %+v", d)
	}
	if _, ok := got[secretmgr.Env]; ok {
		t.Errorf("env listed")
	}
	b, _ := os.ReadFile(log)
	// only harmless version calls ran (ONB 4.8); they run in parallel, so
	// the log order is not fixed
	calls := strings.Split(strings.TrimSpace(string(b)), "\n")
	slices.Sort(calls)
	if strings.Join(calls, "|") != "bw --version|gopass version|op --version" {
		t.Errorf("calls:\n%s", b)
	}
}

// At most Limiter's n probes run at once (ONB 6.1), and a probe that waits
// past its deadline for a slot is a timeout.
func TestLimiter(t *testing.T) {
	s := newSandbox(t)
	fakeBin(t, s.bin, "ssh", `sleep 0.3; echo "Hi mira! You've successfully authenticated" >&2; exit 1`)
	p := s.prober(Options{Limiter: NewLimiter(2)})
	var wg sync.WaitGroup
	start := time.Now()
	for range 6 {
		wg.Go(func() {
			if r := p.SSHAuth(ctxT(t), Route{}); r.Class != ClassOK {
				t.Errorf("probe: %+v", r)
			}
		})
	}
	// 6 probes of 0.3 s, 2 at once: at least 3 rounds
	wg.Wait()
	if el := time.Since(start); el < 800*time.Millisecond {
		t.Errorf("6 probes of 0.3 s at limit 2 took %s: the limit did not hold", el)
	}

	busy := s.prober(Options{Limiter: NewLimiter(1), SSHTimeout: 100 * time.Millisecond})
	hold := busy.o.Limiter
	if err := hold.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer hold.release()
	if r := busy.SSHAuth(ctxT(t), Route{}); r.Class != ClassTimeout {
		t.Errorf("starved probe: %+v", r)
	}
}
