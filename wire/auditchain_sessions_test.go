package wire

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsagent "github.com/thesatellite-ai/lore/ops/agent"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// TestStandaloneRefusesKnownToolSessionID (OV11, later 434): on cmd/lore
// (StandaloneOptions, the chain audit sink) a tool session id this
// machine's session rows know is a finding in row text at write time,
// so `memory add` asks (E_NEEDS_CONFIRM, the id never printed) instead of
// storing it; the same text before the session is known is stored, and a
// short id never matches.
func TestStandaloneRefusesKnownToolSessionID(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	ctx := context.Background()
	repo := gitRepo(t)
	home := filepath.Join(t.TempDir(), ".lore")
	open := func() *App {
		t.Helper()
		a, err := New(StandaloneOptions(repo, home, repo, "test"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	sid := "7d1c0e5b-41aa-4f0e-9b3c-" + strings.Repeat("e", 12)
	body := func(s string) string { return `{"body":"resume with --resume ` + s + ` later"}` }

	a := open()
	withProject(t, a)
	if _, err := run(t, a, opsmemory.OpAdd, body(sid), true); err != nil {
		t.Fatalf("an unknown session id is plain text: %v", err)
	}
	if err := a.RecordSessionHook(ctx, opsagent.HookIn{Tool: "codex", Session: sid, Model: "m"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.RecordSessionHook(ctx, opsagent.HookIn{Tool: "codex", Session: "abc12", Model: "m"}, nil); err != nil {
		t.Fatal(err)
	}

	b := open() // the next command: a new process
	_, err := run(t, b, opsmemory.OpAdd, body(sid), true)
	if !errs.Has(err, errs.NeedsConfirm) || strings.Contains(err.Error(), sid) {
		t.Fatalf("a known session id: %v, want E_NEEDS_CONFIRM without the id", err)
	}
	if _, err = run(t, b, opsmemory.OpAdd, body("abc12"), true); err != nil {
		t.Fatalf("a short session id never matches: %v", err)
	}
}
