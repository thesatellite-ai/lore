package wire

// migratedby_test.go — T-1: `lore migrate run` under an agent records the
// PERSON in _meta.json's migrated_by ("Name <email>") and in the line migrate
// status, doctor and where show; the agent is only in migrated_via (audit),
// as a commit's Lore-Agent trailer. Made-up people only.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
)

func TestMigrateRunRecordsThePersonNotTheAgent(t *testing.T) {
	agentEnv(t)
	home := t.TempDir()
	writeConfig(t, home, "identity: {key: \"human:alice@example.com\", name: Alice Example}\n")
	top := t.TempDir()
	data := filepath.Join(top, ".lore", "data")
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, rowfs.MetaFileName), []byte(`{"_v": 1, "project_id": "p"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wall := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	a := &App{opts: Options{Home: home, RepoDir: top, Wall: func() time.Time { return wall }}}
	ctx := context.Background()
	who, err := resolveIdentity(ctx, a.opts)
	if err != nil {
		t.Fatal(err)
	}
	if who.Agent == "" || !strings.Contains(who.DisplayName, viaTool) {
		t.Fatalf("detection did not see the agent (the case under test): %+v", who)
	}

	line, err := a.raiseProjectFormat(ctx, top)
	if err != nil {
		t.Fatal(err)
	}
	const person = "Alice Example <alice@example.com>"
	if !strings.Contains(line, "(by "+person+", 2026-11-13T10:00:00Z)") || strings.Contains(line, "agent:") || strings.Contains(line, viaTool) {
		t.Errorf("migrate run line names the agent, not the person: %s", line)
	}
	b, err := os.ReadFile(filepath.Join(data, rowfs.MetaFileName))
	if err != nil {
		t.Fatal(err)
	}
	pf, err := ProjectFormatAt(top)
	if err != nil {
		t.Fatal(err)
	}
	if pf.MigratedBy != person {
		t.Errorf("migrated_by = %q, want %q:\n%s", pf.MigratedBy, person, b)
	}
	if !strings.HasPrefix(pf.MigratedVia, "claude_code (agent:auto/claude_code/") {
		t.Errorf("migrated_via = %q, want the agent (audit):\n%s", pf.MigratedVia, b)
	}
	// migrate status, doctor and where all print ProjectFormatLine
	shown := ProjectFormatLine(pf)
	if !strings.Contains(shown, "migrated by "+person+" at 2026-11-13T10:00:00Z") || strings.Contains(shown, "agent:") || strings.Contains(shown, viaTool) {
		t.Errorf("shown line: %s", shown)
	}
}

func TestMigratedBy(t *testing.T) {
	alice := identity.Resolved{StableKey: "human:alice@example.com", DisplayName: "Alice Example", Kind: identity.KindHuman}
	for _, c := range []struct {
		name    string
		who     identity.Resolved
		by, via string
	}{
		{"the person", alice, "Alice Example <alice@example.com>", ""},
		{"an agent for the person", identity.Resolved{StableKey: "agent:auto/codex/abc123", DisplayName: "Alice Example via codex", Kind: identity.KindAgent, Agent: "agent:auto/codex/abc123", Label: "codex", Owner: &alice},
			"Alice Example <alice@example.com>", "codex (agent:auto/codex/abc123)"},
		{"a key with no address", identity.Resolved{StableKey: "human:bob", DisplayName: "Bob Example", Kind: identity.KindHuman}, "Bob Example", ""},
	} {
		if by, via := migratedBy(c.who); by != c.by || via != c.via {
			t.Errorf("%s: by %q via %q, want %q %q", c.name, by, via, c.by, c.via)
		}
	}
}
