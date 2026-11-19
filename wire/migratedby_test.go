package wire

// migratedby_test.go — T-1: `lore migrate run` under an agent records the
// PERSON in _meta.json's migrated_by and in the line migrate status, doctor
// and where show; the agent is only in migrated_via (audit), as a commit's
// Lore-Agent trailer. devco:D-705: the person by display name only, never
// an email or actor id (a client repo's leak check refuses either in the
// migration commit). Made-up people only.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/errs"
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
	const person = "Alice Example"
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
	// devco:D-705: no address and no actor id of the person in the
	// committed file (lore's own pre-commit refused the migration commit)
	if strings.Contains(string(b), "@") || strings.Contains(string(b), "act_") {
		t.Errorf("_meta.json names the person's email or actor id:\n%s", b)
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
		{"the person", alice, "Alice Example", ""},
		{"an agent for the person", identity.Resolved{StableKey: "agent:auto/codex/abc123", DisplayName: "Alice Example via codex", Kind: identity.KindAgent, Agent: "agent:auto/codex/abc123", Label: "codex", Owner: &alice},
			"Alice Example", "codex (agent:auto/codex/abc123)"},
		{"a key with no address", identity.Resolved{StableKey: "human:bob", DisplayName: "Bob Example", Kind: identity.KindHuman}, "Bob Example", ""},
		// devco:D-705: no display name records no one, never the address
		{"a person with no name", identity.Resolved{StableKey: "human:carol@example.com", Kind: identity.KindHuman}, "", ""},
		{"a name that is an address", identity.Resolved{StableKey: "human:dan@example.com", DisplayName: "dan@example.com", Kind: identity.KindHuman}, "", ""},
	} {
		if by, via := migratedBy(c.who); by != c.by || via != c.via {
			t.Errorf("%s: by %q via %q, want %q %q", c.name, by, via, c.by, c.via)
		}
	}
}

// devco:D-705 follow-up: a _meta.json lore 0.1.18 already wrote with
// migrated_by "Name <email>" (left uncommitted after a client repo's hook
// refused it). The next migrate run keeps the name only, every other key
// as it was; its dry run says so and writes nothing; a value with no
// address is left alone; a bare address leaves no migrated_by.
func TestFixMigratedByEmailLeftBy0118(t *testing.T) {
	t.Parallel()
	meta := func(t *testing.T, by string) string {
		t.Helper()
		top := t.TempDir()
		data := filepath.Join(top, ".lore", "data")
		if err := os.MkdirAll(data, 0o750); err != nil {
			t.Fatal(err)
		}
		doc := `{"_v": 2, "format": 2, "project_id": "p", "migrated_by": "` + by + `", "migrated_at": "2026-11-19T10:00:00Z"}` + "\n"
		if err := os.WriteFile(filepath.Join(data, rowfs.MetaFileName), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		return top
	}
	read := func(t *testing.T, top string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(top, ".lore", "data", rowfs.MetaFileName))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	top := meta(t, "Cora Admin <cora@acme.test>")
	before := read(t, top)
	line, err := fixMigratedByEmail(top, true)
	if err != nil || !strings.Contains(line, "would keep the name only (Cora Admin), not written (dry run)") {
		t.Fatalf("dry run line %q, %v", line, err)
	}
	if read(t, top) != before {
		t.Fatal("the dry run wrote _meta.json")
	}
	line, err = fixMigratedByEmail(top, false)
	if err != nil || !strings.Contains(line, "now keeps the name only (Cora Admin)") || strings.Contains(line, "cora@") {
		t.Fatalf("run line %q, %v", line, err)
	}
	pf, err := ProjectFormatAt(top)
	if err != nil {
		t.Fatal(err)
	}
	if after := read(t, top); pf.MigratedBy != "Cora Admin" || pf.MigratedAt != "2026-11-19T10:00:00Z" || strings.Contains(after, "@") || !strings.Contains(after, `"project_id": "p"`) {
		t.Fatalf("after the run: %+v\n%s", pf, after)
	}
	if again, err := fixMigratedByEmail(top, false); err != nil || again != "" {
		t.Fatalf("second run must say nothing: %q, %v", again, err)
	}

	bare := meta(t, "cora@acme.test")
	if line, err := fixMigratedByEmail(bare, false); err != nil || !strings.Contains(line, "no migrated_by") {
		t.Fatalf("bare address: %q, %v", line, err)
	}
	if strings.Contains(read(t, bare), "migrated_by") {
		t.Fatalf("a bare address must leave no migrated_by:\n%s", read(t, bare))
	}

	plain := meta(t, "Cora Admin")
	if line, err := fixMigratedByEmail(plain, false); err != nil || line != "" {
		t.Fatalf("a name only is left alone: %q, %v", line, err)
	}
}

// devco:D-705 follow-up: a plain commit refused for the email in
// _meta.json names the command that rewrites it, not "remove the text".
func TestLeakBlockedMetaEmailNamesMigrateRun(t *testing.T) {
	t.Parallel()
	for _, path := range []string{rowfs.RepoMetaPath, "services/api/" + rowfs.RepoMetaPath} {
		f := leakcheck.Finding{Rule: leakcheck.RuleID, Where: "commit " + path, Path: path, Detail: leakcheck.DetailRosterEmail + "acme/org"}
		err := leakBlocked(LeakWhereCommit, []leakcheck.Finding{f}, false)
		var le *errs.Error
		if !errors.As(err, &le) || !strings.Contains(le.Hint, "run `lore migrate run`, which keeps the name only") {
			t.Fatalf("%s: hint = %v", path, err)
		}
	}
	other := leakcheck.Finding{Rule: leakcheck.RuleID, Where: "commit README.md", Path: "README.md", Detail: leakcheck.DetailRosterEmail + "acme/org"}
	var le *errs.Error
	if err := leakBlocked(LeakWhereCommit, []leakcheck.Finding{other}, false); !errors.As(err, &le) || strings.Contains(le.Hint, "migrate run") {
		t.Fatalf("another file keeps its own fix: %v", err)
	}
}
