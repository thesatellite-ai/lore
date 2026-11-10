package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

type fakeHost struct {
	rows   []Row
	err    error
	reveal bool
	limit  int
}

func (f *fakeHost) ListSessions(_ context.Context, limit int) ([]Row, error) {
	f.limit = limit
	return f.rows, f.err
}
func (f *fakeHost) RevealToolSessions(context.Context) bool { return f.reveal }

func sample() []Row {
	return []Row{
		{ID: "ses_1", ActorID: "act_1", Actor: "tomas via codex", Tool: "codex", ToolSessionID: "019a7c2e-4b1f", ParentSessionID: "outer-1234",
			StartedAt: "2026-11-20T10:02:00Z", LastWriteAt: "2026-11-20T10:41:09Z", Writes: 7, NewKeys: 2},
		{ID: "ses_0", ActorID: "act_2", StartedAt: "2026-11-20T09:15:00Z", Writes: 3, DeclaredHuman: true},
	}
}

// TestListRedaction: raw ids only for an interactive caller in a process
// that may reveal them (OV11-f); every other combination hides them.
func TestListRedaction(t *testing.T) {
	for _, tc := range []struct {
		interactive, reveal, shown bool
	}{{true, true, true}, {true, false, false}, {false, true, false}, {false, false, false}} {
		h := &fakeHost{rows: sample(), reveal: tc.reveal}
		out, err := List(context.Background(), h, tc.interactive)
		if err != nil {
			t.Fatal(err)
		}
		if h.limit != ListLimit {
			t.Fatalf("limit %d", h.limit)
		}
		got := out.Sessions[0]
		if tc.shown != (got.ToolSessionID == "019a7c2e-4b1f" && got.ParentSessionID == "outer-1234") {
			t.Fatalf("%+v: tool session %q parent %q", tc, got.ToolSessionID, got.ParentSessionID)
		}
		if !tc.shown && (got.ToolSessionID != Redacted || got.ParentSessionID != Redacted) {
			t.Fatalf("%+v: not redacted: %+v", tc, got)
		}
		if out.Sessions[1].ToolSessionID != "" {
			t.Fatalf("an empty id stays empty, got %q", out.Sessions[1].ToolSessionID)
		}
		if out.Sessions[1].Actor != "act_2" {
			t.Fatalf("actor falls back to the id: %q", out.Sessions[1].Actor)
		}
	}
}

// TestListText: the CLI_REFERENCE 3.8 columns, minutes, dashes, counts and
// the declared_human flag; the empty list's line.
func TestListText(t *testing.T) {
	out, err := List(context.Background(), &fakeHost{rows: sample()}, false)
	if err != nil {
		t.Fatal(err)
	}
	txt := out.Text()
	lines := strings.Split(strings.TrimSuffix(txt, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines: %q", txt)
	}
	for _, col := range []string{"ID", "ACTOR", "TOOL", "TOOL SESSION", "STARTED", "LAST WRITE", "WRITES", "NEW KEYS", "FLAGS"} {
		if !strings.Contains(lines[0], col) {
			t.Fatalf("header misses %s: %q", col, lines[0])
		}
	}
	for _, want := range []string{"tomas via codex", "<redacted>", "2026-11-20 10:02", "2026-11-20 10:41", " 7 ", " 2 "} {
		if !strings.Contains(lines[1], want) {
			t.Fatalf("row 1 misses %q: %q", want, lines[1])
		}
	}
	if !strings.HasSuffix(lines[2], "declared_human") || !strings.Contains(lines[2], " - ") {
		t.Fatalf("row 2: %q", lines[2])
	}
	if got := (ListOut{}).Text(); got != "(no sessions)\n" {
		t.Fatalf("empty: %q", got)
	}
}

// TestListError: a host failure is E_INTERNAL with the cause kept.
func TestListError(t *testing.T) {
	cause := errors.New("disk")
	_, err := List(context.Background(), &fakeHost{err: cause}, true)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.Internal || !errors.Is(err, cause) {
		t.Fatalf("got %v", err)
	}
}
