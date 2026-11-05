package wire_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// TestHookRefusedAudit (SECRETS 16, FA-AGENT-7, later 36): a commit or
// push check that refuses a secret records one secret.hook_refused event
// per finding (hook, path, rule; never the value or its masked preview),
// a check of anything else records none, and a clean check records none.
func TestHookRefusedAudit(t *testing.T) {
	key := "AKIA" + strings.Repeat("Q", 16)
	cases := []struct {
		name, where string
		docs        map[string]string
		events      []string // wanted Reason suffixes
	}{
		{name: "push one", where: wire.LeakWherePush, docs: map[string]string{"notes.txt": "key " + key},
			events: []string{"hook=pre-push path=notes.txt rule=aws_access_key"}},
		{name: "commit two files", where: wire.LeakWhereCommit, docs: map[string]string{"a.txt": "k " + key, "b/c.md": "x " + key},
			events: []string{"hook=pre-commit path=a.txt rule=aws_access_key", "hook=pre-commit path=b/c.md rule=aws_access_key"}},
		{name: "other check", where: "move", docs: map[string]string{"notes.txt": "key " + key}},
		{name: "clean push", where: wire.LeakWherePush, docs: map[string]string{"notes.txt": "nothing here"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := opstest.New(t)
			ctx := context.Background()
			docs := map[string][]byte{}
			for p, s := range c.docs {
				docs[p] = []byte(s)
			}
			err := h.App.HookSecrets(ctx, h.Repo, c.where, docs)
			blocked := strings.Contains(strings.Join(mapValues(c.docs), " "), key)
			if blocked != errs.Has(err, errs.LeakBlocked) {
				t.Fatalf("err = %v, want blocked=%v", err, blocked)
			}
			got := hookRefusedLines(t, h, key)
			if len(got) != len(c.events) {
				t.Fatalf("hook_refused events = %q, want %d", got, len(c.events))
			}
			for _, w := range c.events {
				found := false
				for _, g := range got {
					found = found || g == w
				}
				if !found {
					t.Fatalf("no event ending %q in %q", w, got)
				}
			}
		})
	}
}

// hookRefusedLines is the secret_result of every secret.hook_refused line
// of the repo's audit-v2.jsonl; a line carrying key or its preview fails.
func hookRefusedLines(t *testing.T, h *opstest.H, key string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.Repo, filepath.FromSlash(wire.AuditFile)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ln := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var rec struct {
			Action string `json:"action"`
			Result string `json:"secret_result"`
		}
		if ln == "" {
			continue
		}
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			t.Fatal(err)
		}
		if rec.Action != string(engine.ActionLeakHookRefused) {
			continue
		}
		if strings.Contains(ln, key) || strings.Contains(ln, scan.Mask(key)) {
			t.Fatalf("event carries the value or its preview: %s", ln)
		}
		got = append(got, rec.Result)
	}
	return got
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
