package renderhost

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/localstate"
)

// TestHookFilePath (FA-CTX-5; fa-ctx later:46): tool_input.file_path wins;
// else the first Update/Add/Delete header of a Codex apply_patch, spaces
// trimmed, an empty header skipped; nothing named is "".
func TestHookFilePath(t *testing.T) {
	cases := []struct {
		name, file, command, want string
	}{
		{"file_path", "src/a.go", "*** Update File: src/b.go", "src/a.go"},
		{"update", "", "*** Begin Patch\n*** Update File: src/b.go\n@@\n-x\n+y\n*** End Patch", "src/b.go"},
		{"add", "", "*** Begin Patch\n*** Add File: docs/new.md\n+hi", "docs/new.md"},
		{"delete", "", "*** Delete File: old.txt", "old.txt"},
		{"indented, first of two", "", "  *** Update File:  web/x.ts  \n*** Add File: web/y.ts", "web/x.ts"},
		{"empty header skipped", "", "*** Update File: \n*** Add File: z.go", "z.go"},
		{"no header", "", "ls -la", ""},
		{"nothing", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var in hookInput
			in.ToolInput.FilePath, in.ToolInput.Command = c.file, c.command
			if got := in.hookFilePath(); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
	for tool, want := range map[string]string{toolprofile.NameGeminiCLI: "AfterTool", toolprofile.NameClaudeCode: "PreToolUse", toolprofile.NameCodex: "PreToolUse"} {
		if got := hookDefaultEvent(tool); got != want {
			t.Errorf("%s default event %q, want %q", tool, got, want)
		}
	}
}

// TestHookOutputAndUsage (6.8, 6.9, FA-CTX-3/5; fa-ctx later:46): a Gemini
// CLI file hook without an event name answers AfterTool, a Codex
// apply_patch hook PreToolUse, each with the matching rule in
// additionalContext; every row sent is recorded in local state, its count
// growing per send (no session id: nothing is muted).
func TestHookOutputAndUsage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	a, project := repoApp(t)
	w, err := a.Create(ctx, store.SourceRepo, schema.TableRules, map[string]value.Value{
		"body": text(t, "Edit src/pay.go only with a ledger test."), "severity": text(t, "must"),
		"activation": text(t, schema.RuleActivationAlways), "source_kind": text(t, "manual"), "project_id": text(t, project)})
	if err != nil {
		t.Fatal(err)
	}
	id := w.Row.ID.String()
	h := New(Options{App: a})
	for i, c := range []struct {
		tool, stdin, event string
	}{
		{toolprofile.NameGeminiCLI, `{"tool_input":{"file_path":"src/pay.go"}}`, "AfterTool"},
		{toolprofile.NameCodex, `{"tool_input":{"command":"*** Begin Patch\n*** Update File: src/pay.go\n*** End Patch"}}`, "PreToolUse"},
	} {
		var out bytes.Buffer
		if err = h.runContextHook(ctx, &contextFlags{hook: c.tool, hookStdin: true}, strings.NewReader(c.stdin), &out); err != nil {
			t.Fatal(err)
		}
		var got hookOutput
		if err = json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("%s output %q: %v", c.tool, out.String(), err)
		}
		if got.HookSpecificOutput.HookEventName != c.event || !strings.Contains(got.HookSpecificOutput.AdditionalContext, id) {
			t.Fatalf("%s output %+v", c.tool, got)
		}
		s, serr := a.LocalState(ctx)
		if serr != nil {
			t.Fatal(serr)
		}
		u, found, gerr := localstate.GetJSON[rowUsage](ctx, s, localstate.Usage, id)
		_ = s.Close()
		if gerr != nil || !found || u.Count != i+1 || u.LastUsed.IsZero() {
			t.Fatalf("usage after send %d: %+v found %v err %v", i+1, u, found, gerr)
		}
	}
	// recordUsage with no ids or no app is a no-op, never a panic.
	h.recordUsage(ctx, nil)
	New(Options{}).recordUsage(ctx, []string{id})
}
