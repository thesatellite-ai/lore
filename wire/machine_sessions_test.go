package wire_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/wire"
)

// toolSession is a raw tool session id as Codex sets it (long enough for
// the leak rule).
const toolSession = "0199aaaa-bbbb-cccc-dddd-eeeeffff0000"

// TestSessionsThroughTheMachineStore: a session is created once per
// (actor, tool, tool session) in the machine store with its raw tool id
// (never refused by the leak rule, never in the repo); a later write moves
// ended_at, fills only empty fields, merges sources and flags; Sessions is
// newest first and survives a reopen; KnownToolSessionIDs leaves out short
// ids and synthetic keys (P3-LOCAL-5, AGENT_IDENTITY 6.2).
func TestSessionsThroughTheMachineStore(t *testing.T) {
	ctx := context.Background()
	h := opstest.New(t)
	d := wire.SessionDetails{ActorID: opstest.Actor, Tool: "codex", ToolSessionID: toolSession, Model: "",
		Signal: "CODEX_THREAD_ID", Sources: map[string]string{"tool_session_id": "CODEX_THREAD_ID"}, Flags: []string{"tty"}}
	id, err := h.App.FindOrCreateSession(ctx, "prj_a", d)
	if err != nil {
		t.Fatal(err)
	}
	again := d
	again.Model, again.Signal = "gpt-5", "other"
	again.Sources = map[string]string{"model": "session_hook", "tool_session_id": "other"}
	again.Flags = []string{"tty", "weak_only"}
	if id2, aerr := h.App.FindOrCreateSession(ctx, "prj_a", again); aerr != nil || id2 != id {
		t.Fatalf("second write = %s, %v; want the same session %s", id2, aerr, id)
	}
	if _, err = h.App.FindOrCreateSession(ctx, "prj_a", wire.SessionDetails{ActorID: opstest.Actor, Tool: "aider", ToolSessionID: "synthetic:aider:42"}); err != nil {
		t.Fatal(err)
	}
	if _, err = h.App.FindOrCreateSession(ctx, "prj_b", wire.SessionDetails{ActorID: opstest.Actor, Tool: "x", ToolSessionID: "short"}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(h.Home, filepath.FromSlash(wire.MachineStoreFile))); err != nil {
		t.Fatal(err)
	}
	if got, _ := filepath.Glob(filepath.Join(h.Repo, ".lore", "data", "sessions", "*")); len(got) != 0 {
		t.Fatalf("sessions reached the repo: %v", got)
	}
	for _, app := range []*wire.App{h.App, h.Reopen().App} {
		all, lerr := app.Sessions(ctx, 0)
		if lerr != nil || len(all) != 3 {
			t.Fatalf("Sessions = %d, %v; want 3", len(all), lerr)
		}
		var s wire.SessionRow
		for _, r := range all {
			if r.ID == id {
				s = r
			}
		}
		if s.Model != "gpt-5" || s.Signal != "CODEX_THREAD_ID" || s.ToolSessionID != toolSession || s.ProjectID != "prj_a" {
			t.Errorf("session = %+v; want the model filled, the signal kept, the raw id", s)
		}
		if s.Sources["model"] != "session_hook" || s.Sources["tool_session_id"] != "CODEX_THREAD_ID" || !slices.Equal(s.Flags, []string{"tty", "weak_only"}) {
			t.Errorf("merged sources %v flags %v", s.Sources, s.Flags)
		}
		if s.StartedAt.IsZero() || s.EndedAt.Before(s.StartedAt) {
			t.Errorf("times %v .. %v", s.StartedAt, s.EndedAt)
		}
		if one, _ := app.Sessions(ctx, 1); len(one) != 1 || one[0].EndedAt.Before(all[len(all)-1].EndedAt) {
			t.Errorf("limit 1 = %+v; want the newest", one)
		}
		known, kerr := app.KnownToolSessionIDs(ctx)
		if kerr != nil || !slices.Equal(known, []string{toolSession}) {
			t.Errorf("KnownToolSessionIDs = %v, %v; want only the raw long id", known, kerr)
		}
	}
}
