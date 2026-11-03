package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dbent/gen/ent"
	"saas/pkg/aicoder/mergewatch"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
)

// failingBoard is a claims board that cannot be read.
type failingBoard struct{ err error }

func (b failingBoard) Local(context.Context) (signals.View, error) { return signals.View{}, b.err }

// TestFilterTeamRowsBoardReadFails: with --mine or --claimed-by, a board
// that cannot be read is the command's error (never every row shown as
// matching); without filters the rows are shown as they are.
func TestFilterTeamRowsBoardReadFails(t *testing.T) {
	rows := []*ent.Task{{ID: "tsk_1", Title: "a"}, {ID: "tsk_2", Title: "b"}}
	boom := errors.New("signals ref unreadable")
	cases := []struct {
		name    string
		flags   taskListTeamFlags
		wantErr bool
	}{
		{"--mine", taskListTeamFlags{mine: true}, true},
		{"--claimed-by", taskListTeamFlags{claimedBy: "@alice"}, true},
		{"--mine and --claimed-by", taskListTeamFlags{mine: true, claimedBy: "@alice"}, true},
		{"no filter", taskListTeamFlags{}, false},
		{"--all-repos only", taskListTeamFlags{allRepos: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, states, err := filterTeamRows(t.Context(), nil, &tc.flags, rows, failingBoard{err: boom}, "acme", "act_me")
			if tc.wantErr {
				if !errors.Is(err, boom) || out != nil {
					t.Fatalf("want the board error and no rows, got rows=%d err=%v", len(out), err)
				}
				return
			}
			if err != nil || len(out) != len(rows) || states != nil {
				t.Fatalf("no filter: want every row, no states, no error; got rows=%d states=%v err=%v", len(out), states, err)
			}
		})
	}
}

// fakeStager stages rows (or fails).
type fakeStager struct{ err error }

func (s fakeStager) Stage(context.Context, ...coord.RowWrite) ([]signals.Companion, error) {
	return nil, s.err
}

// fakeSignals records whether a write happened (or fails it).
type fakeSignals struct {
	err   error
	wrote bool
}

func (s *fakeSignals) Write(context.Context, signals.Decide) (signals.Result, error) {
	s.wrote = s.err == nil
	return signals.Result{}, s.err
}

// TestRecordMergedReportsFailures: a stage or write failure is returned
// (nothing written after a failed stage), and the report never says
// "✓ merged" for it; a success says "✓ merged" and nothing else.
func TestRecordMergedReportsFailures(t *testing.T) {
	f := mergewatch.Finding{Method: mergewatch.MethodTrailer, Commit: "0123456789abcdef", At: time.Date(2026, 11, 3, 10, 30, 0, 0, time.UTC)}
	next := coord.Doc{"id": "tsk_1"}
	cases := []struct {
		name               string
		stageErr, writeErr error
		wantWrote, wantOK  bool
	}{
		{"stage fails", errors.New("stage boom"), nil, false, false},
		{"write fails", nil, errors.New("push rejected"), false, false},
		{"recorded", nil, nil, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sw := &fakeSignals{err: tc.writeErr}
			err := recordMerged(t.Context(), fakeStager{err: tc.stageErr}, sw, "tsk_1", next)
			if (err == nil) != tc.wantOK || sw.wrote != tc.wantWrote {
				t.Fatalf("err=%v wrote=%v; want ok=%v wrote=%v", err, sw.wrote, tc.wantOK, tc.wantWrote)
			}
			var out bytes.Buffer
			reportMerged(&out, "tsk_1", f, err)
			got := out.String()
			if tc.wantOK != strings.HasPrefix(got, "✓ tsk_1 merged") {
				t.Fatalf("report %q: \"✓ merged\" must appear only when recorded", got)
			}
			if !tc.wantOK && !strings.Contains(got, "could not be recorded") {
				t.Fatalf("a failed record must say so: %q", got)
			}
		})
	}
}
