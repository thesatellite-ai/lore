package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/wire"
)

// downSink is an audit sink that cannot record (a full disk).
type downSink struct{}

func (downSink) Record(context.Context, engine.Event) error {
	return errors.New("audit disk full")
}

// auditJSON is the --json body of a write as far as this test reads it.
type auditJSON struct {
	ID       string `json:"id"`
	Store    string `json:"store"`
	Warnings []struct {
		Code    string `json:"code"`
		Field   string `json:"field"`
		Message string `json:"message"`
	} `json:"warnings"`
}

// TestAuditDeferredJSON (P2-AUDIT-2, later 83 --json half): when the audit
// sink fails after a journaled write — the repo's (Options.Audit down) or
// an attached store's (its machine-local audit folder cannot be made) —
// memory add succeeds and its --json body carries the row's id and store
// and exactly one warning {"code":"W_AUDIT_DEFERRED","field":"",
// "message":…} naming the row and the cause; a healthy write has no
// warnings key at all.
func TestAuditDeferredJSON(t *testing.T) {
	check := func(t *testing.T, h *opstest.H, input, store, cause string) {
		t.Helper()
		res, err := h.Run(memory.OpAdd, input)
		if err != nil {
			t.Fatalf("a deferred audit is not an error: %v", err)
		}
		body := h.JSON(res)
		var out auditJSON
		if err = json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(out.ID, "mem_") || out.Store != store || len(out.Warnings) != 1 {
			t.Fatalf("json %s", body)
		}
		w := out.Warnings[0]
		if w.Code != string(engine.WarnAuditDeferred) || w.Field != "" || !strings.Contains(w.Message, out.ID) || !strings.Contains(w.Message, cause) {
			t.Fatalf("warning %+v, want W_AUDIT_DEFERRED naming %s and %q", w, out.ID, cause)
		}
	}

	t.Run("repo", func(t *testing.T) {
		h := opstest.New(t, func(o *wire.Options) { o.Audit = downSink{} })
		check(t, h, `{"body":"audited"}`, "repo", "audit disk full")
	})

	t.Run("attached store", func(t *testing.T) {
		h := opstest.WithStores(t, []string{"kb"})
		healthy := h.JSON(h.Must(memory.OpAdd, `{"body":"healthy","to":"kb"}`))
		if strings.Contains(healthy, `"warnings"`) {
			t.Fatalf("a healthy write has warnings: %s", healthy)
		}
		// the store's audit folder cannot be made: a file sits at its path
		dir := filepath.Join(h.Home, filepath.FromSlash(wire.StoreAuditDir))
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir, []byte("not a folder"), 0o600); err != nil {
			t.Fatal(err)
		}
		check(t, h, `{"body":"to kb","to":"kb"}`, "kb", "audit folder")
	})
}

// TestAuditDeferredHumanLine (T-TESTS-2): without --json, memory add with
// a down audit sink prints its success line and then exactly one
// "warning: … (W_AUDIT_DEFERRED)" line naming the row; a healthy write
// prints the success line and nothing more.
func TestAuditDeferredHumanLine(t *testing.T) {
	healthy := opstest.New(t)
	got := opstest.Text(healthy.Must(memory.OpAdd, `{"body":"fine"}`))
	if strings.Contains(got, "warning:") || strings.Contains(got, "W_") {
		t.Fatalf("a healthy add prints a warning: %q", got)
	}
	plain := strings.Count(got, "\n")

	h := opstest.New(t, func(o *wire.Options) { o.Audit = downSink{} })
	res := h.Must(memory.OpAdd, `{"body":"audited"}`)
	id := h.ID(res)
	txt := opstest.Text(res)
	lines := strings.Split(strings.TrimSuffix(txt, "\n"), "\n")
	if len(lines) != plain+1 || !strings.HasPrefix(lines[0], "✓ "+id) {
		t.Fatalf("human output %q, want the %d healthy lines + one warning", txt, plain)
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "warning: the audit record of "+id) || !strings.HasSuffix(last, "audit disk full (W_AUDIT_DEFERRED)") {
		t.Fatalf("warning line %q", last)
	}
}
