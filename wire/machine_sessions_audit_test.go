package wire_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/wire"
)

// sessionSink is an audit sink shaped like the old binary's coreAuditSink:
// its Session finds or creates the process's session row through the same
// App, from inside the App's own Record call, once per process.
type sessionSink struct {
	app  *wire.App
	once sync.Once
	ses  string
	err  error
	mu   sync.Mutex
	evs  []engine.Event
}

func (s *sessionSink) Record(_ context.Context, ev engine.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evs = append(s.evs, ev)
	return nil
}

func (s *sessionSink) Session(ctx context.Context) (string, error) {
	if s.app == nil {
		return "", nil // the harness seeds its project before the test wires the App in
	}
	s.once.Do(func() {
		s.ses, s.err = s.app.FindOrCreateSession(ctx, "prj_a", wire.SessionDetails{ActorID: opstest.Actor, Tool: "codex", ToolSessionID: toolSession})
	})
	return s.ses, s.err
}

func (s *sessionSink) ToolSessionIDs(ctx context.Context) ([]string, error) {
	if s.app == nil {
		return nil, nil
	}
	return s.app.KnownToolSessionIDs(ctx)
}

// writeWithin runs fn and fails the test when it has not returned within
// d: a deadlock shows as this failure, not as a hung suite.
func writeWithin(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %s: the session write from inside the audit sink deadlocked", what, d)
	}
}

// TestSessionWriteFromInsideTheAuditSink: the first write of a process
// creates its session row from inside the audit sink (routedAudit →
// Session → App.FindOrCreateSession → engine Create on the machine store)
// without deadlocking: the engine holds no lock across AuditSink.Record,
// and the session row's own event is not linked again, so the sink's once
// is not re-entered (P3-LOCAL-5). Every event of the repo write carries
// the ses_ id, the session row exists once, later writes reuse it, and the
// leak rule now knows the raw tool session id (a row holding it is
// refused). Run it with -race.
func TestSessionWriteFromInsideTheAuditSink(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sink := &sessionSink{}
	h := opstest.New(t, func(o *wire.Options) { o.Audit = sink })
	sink.mu.Lock()
	sink.app, sink.evs = h.App, nil // the harness's seed writes ran before the session source was wired
	sink.mu.Unlock()
	writeWithin(t, 30*time.Second, "memory.add", func() { h.Must("memory.add", `{"body":"first write"}`) })
	writeWithin(t, 30*time.Second, "second memory.add", func() { h.Must("memory.add", `{"body":"second write"}`) })
	if sink.err != nil || sink.ses == "" {
		t.Fatalf("session = %q, %v", sink.ses, sink.err)
	}
	all, err := h.App.Sessions(ctx, 0)
	if err != nil || len(all) != 1 || all[0].ID != sink.ses {
		t.Fatalf("Sessions = %+v, %v; want the one session %s", all, err, sink.ses)
	}
	sink.mu.Lock()
	var repo, sessions int
	for _, ev := range sink.evs {
		switch {
		case string(ev.Table) == "sessions":
			sessions++
			if ev.Session != "" {
				t.Errorf("the session row's own event is linked to %q", ev.Session)
			}
		case ev.Session != sink.ses:
			t.Errorf("event %s %s has session %q, want %s", ev.Table, ev.ID, ev.Session, sink.ses)
		default:
			repo++
		}
	}
	sink.mu.Unlock()
	if repo < 2 || sessions < 1 {
		t.Fatalf("events: %d linked, %d session; want both writes linked and the session row audited", repo, sessions)
	}
	// concurrent writes of one App after the session exists
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h.App.FindOrCreateSession(ctx, "prj_a", wire.SessionDetails{ActorID: opstest.Actor, Tool: "codex", ToolSessionID: toolSession})
		}()
	}
	writeWithin(t, 30*time.Second, "concurrent session finds", wg.Wait)
	if _, rerr := h.Run("memory.add", `{"body":"leaks `+toolSession+` here"}`); rerr == nil {
		t.Fatal("a memory holding the raw tool session id was stored; the leak rule must refuse it")
	}
}
