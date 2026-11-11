package wire_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/internal/testenv"
	opsagent "github.com/thesatellite-ai/lore/ops/agent"
	opswhoami "github.com/thesatellite-ai/lore/ops/whoami"
	"github.com/thesatellite-ai/lore/wire"
)

// hookModel is the model the hook reports in these tests.
const hookModel = "claude-opus-test"

// withStandaloneSessionHosts installs cmd/lore's audit sink, whoami host
// and agent start host from StandaloneOptions, so New binds them to the
// harness App as it does for cmd/lore.
func withStandaloneSessionHosts(o *wire.Options) {
	s := wire.StandaloneOptions(o.RepoDir, o.Home, o.RepoDir, "test")
	o.Audit, o.Whoami, o.Sessions.Agent = s.Audit, s.Whoami, s.Sessions.Agent
}

// claudeSessionEnv makes the detector see a Claude Code session with
// toolSession as its id and no model (HOME has no settings file).
func claudeSessionEnv(t *testing.T) {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", toolSession)
	for _, k := range []string{"ANTHROPIC_MODEL", "CLAUDE_MODEL", "LORE_NO_DETECT", "LORE_HUMAN", "LORE_SESSION"} {
		t.Setenv(k, "")
	}
}

// TestAgentStartHookRecordsTheModel: `lore agent start --tool claude_code
// --hook-stdin` on cmd/lore's host records the hook's session id and model
// on the session row in the machine store, sources session_hook, prints
// nothing; a second run fills no filled field (run twice); whoami then
// shows the model "(from: session hook)" (AGENT_IDENTITY 6.3-6.4, OV11).
func TestAgentStartHookRecordsTheModel(t *testing.T) {
	ctx := context.Background()
	claudeSessionEnv(t)
	h := opstest.New(t, withStandaloneSessionHosts)
	in := opsagent.HookIn{Tool: "claude_code", Stdin: true}
	stdin := `{"session_id":"` + toolSession + `","model":"` + hookModel + `"}`
	if err := h.App.RecordSessionHook(ctx, in, strings.NewReader(stdin)); err != nil {
		t.Fatal(err)
	}
	if err := h.App.RecordSessionHook(ctx, opsagent.HookIn{Tool: "claude_code", Session: toolSession, Model: "other"}, nil); err != nil {
		t.Fatal(err)
	}
	all, err := h.App.Sessions(ctx, 0)
	if err != nil || len(all) != 1 {
		t.Fatalf("Sessions = %+v, %v; want one row", all, err)
	}
	s := all[0]
	if s.Tool != "claude_code" || s.ToolSessionID != toolSession || s.Model != hookModel || s.Sources["model"] != wire.SessionSourceHook {
		t.Fatalf("session = %+v; want the hook's model kept with source session_hook", s)
	}
	if got := h.App.HookReportedModel(ctx, "claude_code", toolSession); got != hookModel {
		t.Fatalf("HookReportedModel = %q", got)
	}
	out := h.JSON(h.Must(opswhoami.OpShow, `{}`))
	if !strings.Contains(out, `"model":"`+hookModel+`"`) || !strings.Contains(out, `"model":"session_hook"`) {
		t.Fatalf("whoami --json lacks the hook model: %s", out)
	}
	if txt := opstest.Text(h.Must(opswhoami.OpShow, `{}`)); !strings.Contains(txt, hookModel+"  (from: session hook)") {
		t.Fatalf("whoami lines lack the hook model: %s", txt)
	}
}

// TestAgentStartHookThroughTheOperation: agent.start --tool runs the host's
// hook (a row appears) and a broken hook input is one stderr line, never
// an error (6.4: a hook must not stop the tool's session).
func TestAgentStartHookThroughTheOperation(t *testing.T) {
	ctx := context.Background()
	claudeSessionEnv(t)
	h := opstest.New(t, withStandaloneSessionHosts)
	h.Must(opsagent.OpStart, `{"tool":"codex","session":"`+toolSession+`","model":"gpt-5"}`)
	all, err := h.App.Sessions(ctx, 0)
	var codex []wire.SessionRow
	for _, r := range all {
		if r.Tool == "codex" {
			codex = append(codex, r)
		}
	}
	// the detected claude_code session also gets its row (the hook row's
	// own audit event links this process's session); only codex is the hook's
	if err != nil || len(codex) != 1 || codex[0].Model != "gpt-5" || codex[0].Signal != wire.SessionSignalHook || codex[0].Sources["model"] != wire.SessionSourceHook {
		t.Fatalf("Sessions = %+v, %v; want one hook-only codex row", all, err)
	}
	var stderr bytes.Buffer
	host := wire.StandaloneAgentForTest(h.App, strings.NewReader("not json"), &stderr)
	host.RecordHook(ctx, opsagent.HookIn{Tool: "claude_code", Stdin: true})
	if line := stderr.String(); !strings.HasPrefix(line, "lore agent start: the claude_code hook input is not the expected JSON") || strings.Count(line, "\n") != 1 {
		t.Fatalf("stderr = %q; want one line", line)
	}
	stderr.Reset()
	host.RecordHook(ctx, opsagent.HookIn{Tool: "nobody", Stdin: true})
	if !strings.Contains(stderr.String(), "no confirmed session hook input for nobody") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
