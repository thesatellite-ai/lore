package wire

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// sinkLog is an injected engine.AuditSink (what the old binary passes to
// append the v0.1.17 audit_logs chain).
type sinkLog struct {
	mu  sync.Mutex
	evs []engine.Event
}

func (s *sinkLog) Record(_ context.Context, ev engine.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evs = append(s.evs, ev)
	return nil
}

// TestAuditSinkInjected: with Options.Audit set every core write (create,
// update, delete) reaches the sink in order, with store, table, id and
// actor, and AuditFile is never written.
func TestAuditSinkInjected(t *testing.T) {
	repo := gitRepo(t)
	sink := &sinkLog{}
	a := openWith(t, repo, Options{Audit: sink, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	id := auditWrites(t, a)
	sink.mu.Lock()
	evs := append([]engine.Event(nil), sink.evs...)
	sink.mu.Unlock()
	var mine []engine.Action
	for _, ev := range evs {
		if ev.ID == id {
			mine = append(mine, ev.Action)
			if ev.Store != store.SourceRepo || ev.Table != schema.TableProjects || ev.Actor.IsZero() {
				t.Fatalf("event %+v lacks store/table/actor", ev)
			}
		}
	}
	if strings.Join(actions(mine), ",") != "create,update,delete" {
		t.Fatalf("sink got %v for %s, want create,update,delete", mine, id)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(AuditFile))); !os.IsNotExist(err) {
		t.Fatalf("AuditFile written although a sink is set: %v", err)
	}
}

// TestAuditFileFallback: without a sink the events go to AuditFile, one
// JSON line each, owner-only, and no line carries a field value (the
// project's name never appears, only names, ids and counts).
func TestAuditFileFallback(t *testing.T) {
	repo := gitRepo(t)
	a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	id := auditWrites(t, a)
	path := filepath.Join(repo, filepath.FromSlash(AuditFile))
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != auditFileMode {
		t.Fatalf("audit file mode %v, want %v", fi.Mode().Perm(), os.FileMode(auditFileMode))
	}
	raw := string(mustRead(t, path))
	if strings.Contains(raw, auditSecretName) || strings.Contains(raw, auditRenamed) {
		t.Fatal("the audit file carries a field value")
	}
	var got []string
	for _, ln := range strings.Split(strings.TrimSpace(raw), "\n") {
		var l auditLine
		if jerr := json.Unmarshal([]byte(ln), &l); jerr != nil {
			t.Fatalf("line %q: %v", ln, jerr)
		}
		if l.ID == id.String() {
			got = append(got, l.Action)
		}
	}
	if strings.Join(got, ",") != "create,update,delete" {
		t.Fatalf("file lines for %s = %v", id, got)
	}
}

// auditSecretName and auditRenamed are field values that must never reach
// an audit record.
const (
	auditSecretName = "audit-name-ü-7731"
	auditRenamed    = "audit-renamed-9112"
)

// auditWrites creates, updates and deletes one repo project through the
// App and returns its id.
func auditWrites(t *testing.T, a *App) ids.AnyID {
	t.Helper()
	ctx := background()
	n, err := value.TextOf(auditSecretName)
	if err != nil {
		t.Fatal(err)
	}
	w, err := a.Create(ctx, store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": n})
	if err != nil {
		t.Fatal(err)
	}
	r, err := value.TextOf(auditRenamed)
	if err != nil {
		t.Fatal(err)
	}
	ref := engine.Ref{Store: store.SourceRepo, Table: schema.TableProjects, ID: w.Row.ID}
	if _, err = a.Update(ctx, ref, map[string]value.Value{"name": r}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	return w.Row.ID
}

func actions(as []engine.Action) []string {
	out := make([]string, len(as))
	for i, x := range as {
		out[i] = string(x)
	}
	return out
}
