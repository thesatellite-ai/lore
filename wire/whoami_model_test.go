package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// TestWhoamiModelLine (OV28): the model line of `lore whoami` for an agent
// says where the model came from: the session hook's report, a config
// variable, or "(not exposed by the tool)"; a hook-sourced model with no
// value reads as not exposed; no agent prints no model line at all.
func TestWhoamiModelLine(t *testing.T) {
	t.Parallel()
	modelLine := func(lines []string) string {
		for _, l := range lines {
			if strings.HasPrefix(l, "model ") {
				return strings.TrimSpace(strings.TrimPrefix(l, "model"))
			}
		}
		return ""
	}
	for _, c := range []struct {
		name string
		r    *agentdetect.Result
		want string
	}{
		{"hook", &agentdetect.Result{Tool: "claude_code", Signal: "CLAUDE_CODE_CHILD_SESSION", Model: "claude-opus-test", Sources: map[string]string{sourceFieldModel: SessionSourceHook}}, "claude-opus-test  (from: session hook)"},
		{"config", &agentdetect.Result{Tool: "goose", Signal: "GOOSE_TERMINAL", Model: "gpt-x", ModelFromConfig: true, Sources: map[string]string{sourceFieldModel: agentdetect.SourceConfig}}, "gpt-x  (from config)"},
		{"not exposed", &agentdetect.Result{Tool: "codex", Signal: "CODEX_THREAD_ID"}, "(not exposed by the tool)"},
		{"hook source, empty model", &agentdetect.Result{Tool: "codex", Signal: "CODEX_THREAD_ID", Sources: map[string]string{sourceFieldModel: SessionSourceHook}}, "(not exposed by the tool)"},
		{"no agent", &agentdetect.Result{}, ""},
		{"nil", nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := modelLine(agentLines(c.r)); got != c.want {
				t.Fatalf("model line %q, want %q", got, c.want)
			}
		})
	}
}
