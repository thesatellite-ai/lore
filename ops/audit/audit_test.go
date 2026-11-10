package audit

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// fakeEnv is an in-memory chain: entries newest first plus a canned report.
type fakeEnv struct {
	entries   []Entry
	report    Report
	lastLimit int
}

func (f *fakeEnv) AuditLog(_ context.Context, limit int) ([]Entry, error) {
	f.lastLimit = limit
	if limit > 0 && limit < len(f.entries) {
		return f.entries[:limit], nil
	}
	return f.entries, nil
}

func (f *fakeEnv) AuditVerify(context.Context) (Report, error) { return f.report, nil }

func TestLogLimitMatrix(t *testing.T) {
	cases := []struct {
		name      string
		in        LogIn
		wantLimit int
		wantCode  errs.Code
	}{
		{"default is 50", LogIn{}, DefaultLimit, ""},
		{"explicit limit", LogIn{Limit: 3}, 3, ""},
		{"all wins over limit", LogIn{Limit: 3, All: true}, 0, ""},
		{"negative refused", LogIn{Limit: -1}, 0, errs.InvalidInput},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := &fakeEnv{}
			out, err := logRun(context.Background(), env, c.in)
			if c.wantCode != "" {
				if errs.CodeOf(err) != c.wantCode {
					t.Fatalf("code = %v, want %v", errs.CodeOf(err), c.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if env.lastLimit != c.wantLimit {
				t.Fatalf("limit = %d, want %d", env.lastLimit, c.wantLimit)
			}
			if out.Data == nil || out.Kind != KindLog || out.SchemaVersion != SchemaVersion {
				t.Fatalf("envelope = %+v", out)
			}
		})
	}
}

func TestLogTextEmptyAndForced(t *testing.T) {
	if got := (LogOut{}).Text(); got != "· audit log is empty\n" {
		t.Fatalf("empty text = %q", got)
	}
	out := LogOut{Data: []Entry{
		{CreatedAt: "2026-11-05T10:00:00Z", Action: "secret.reveal", TargetTable: "memories", TargetID: "mem_1", ActorID: "act_1", Reason: "show FORCED (debugging)"},
		{CreatedAt: "2026-11-05T09:00:00Z", Action: "memories.create", TargetTable: "memories", TargetID: "mem_1", ActorID: "act_1"},
	}}
	txt := out.Text()
	if !strings.Contains(txt, "! FORCED (debugging)") || strings.Count(txt, "FORCED") != 1 {
		t.Fatalf("forced line missing or repeated:\n%s", txt)
	}
	if !strings.Contains(txt, "memories/mem_1  by act_1") {
		t.Fatalf("row line shape:\n%s", txt)
	}
}

func TestVerifyMatrix(t *testing.T) {
	cases := []struct {
		name     string
		rep      Report
		wantCode errs.Code
		wantText string
	}{
		{"intact", Report{Entries: 4}, "", "✓ audit chain intact (4 entries)\n"},
		{"intact with external writes", Report{Entries: 4, ExternalWrites: 2}, "", "! 2 change(s) were made outside lore"},
		{"broken link", Report{Entries: 4, BrokenAt: "aud_3"}, errs.AuditChainBroken, ""},
		{"changed outside", Report{Entries: 4, ChangedOutside: []RowRef{{Table: "memories", ID: "mem_1"}}}, errs.AuditChainBroken, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := verify(context.Background(), &fakeEnv{report: c.rep})
			if c.wantCode != "" {
				if errs.CodeOf(err) != c.wantCode {
					t.Fatalf("code = %v, want %v", errs.CodeOf(err), c.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !out.Data.OK || out.Count != c.rep.Entries || !strings.Contains(out.Text(), c.wantText) {
				t.Fatalf("out = %+v text %q", out, out.Text())
			}
		})
	}
}

func TestIsAuditOp(t *testing.T) {
	for name, want := range map[string]bool{OpLog: true, OpVerify: true, "audit": false, "memory.add": false} {
		if IsAuditOp(name) != want {
			t.Fatalf("IsAuditOp(%q) != %v", name, want)
		}
	}
}
