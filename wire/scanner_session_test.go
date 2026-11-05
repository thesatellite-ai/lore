package wire

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// sessionSink is an audit sink that also knows tool session ids (what the
// old binary's lore.db sink offers, ToolSessionSource).
type sessionSink struct {
	mu  sync.Mutex
	ids []string
	err error // a failing read (a damaged lore.db)
	evs []engine.Event
}

func (s *sessionSink) Record(_ context.Context, ev engine.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evs = append(s.evs, ev)
	return nil
}

func (s *sessionSink) ToolSessionIDs(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return append([]string(nil), s.ids...), nil
}

func (s *sessionSink) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *sessionSink) set(ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = ids
}

// newSessionApp is newTestApp with sink as the repo audit sink.
func newSessionApp(t *testing.T, dir string, sink *sessionSink) *App {
	t.Helper()
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	// an internal repo: a redacted ${secret:unstored} may live in it (a
	// client repo refuses every secret reference, BATCH 5)
	a, err := New(Options{RepoDir: dir, Home: t.TempDir(), Actor: actor, Audit: sink,
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a
}

// memBody is the JSON input of memory.add with body.
func memBody(t *testing.T, body string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSessionIDFinding (OV11, P2-CTX-2, AGENT_IDENTITY 9): a raw tool
// session id this machine knows is a secret finding in row text and goes
// through secrets.on_detect like any other: refuse refuses, ask asks
// (E_NEEDS_CONFIRM without --yes), redact stores ${secret:unstored}. An id
// shorter than 8 characters is ignored; an id that starts after the App
// opened is picked up by the next write; the id is never in an error.
func TestSessionIDFinding(t *testing.T) {
	sid := "0f8e2c1a-77b4-4c55-9d0e-" + strings.Repeat("a", 12)
	body := "resume with claude --resume " + sid + " later"
	cases := []struct {
		mode policy.OnDetect // "" = default (ask)
		yes  bool
		code errs.Code // "" = stored
		want string    // stored body
	}{
		{mode: policy.OnDetectRefuse, yes: true, code: errs.SecretDetected},
		{mode: "", code: errs.NeedsConfirm},
		{mode: "", yes: true, code: errs.NeedsConfirm}, // FB-SEC-1: a bare --yes is no choice
		{mode: policy.OnDetectRedact, want: "resume with claude --resume " + refs.Unstored + " later"},
	}
	for _, c := range cases {
		t.Run(string(c.mode)+"/yes="+map[bool]string{true: "1", false: "0"}[c.yes], func(t *testing.T) {
			sink := &sessionSink{}
			a := newSessionApp(t, gitRepo(t), sink)
			withProject(t, a)
			if c.mode != "" {
				addPolicy(t, a, policy.KeyOnDetect, `"`+string(c.mode)+`"`)
			}
			if _, err := run(t, a, opsmemory.OpAdd, memBody(t, body), c.yes); err != nil {
				t.Fatalf("an unknown session id must not be a finding: %v", err)
			}
			sink.set("short7x", sid) // starts after open: the next write reads it
			out, err := run(t, a, opsmemory.OpAdd, memBody(t, body), c.yes)
			if c.code != "" {
				if !errs.Has(err, c.code) {
					t.Fatalf("err = %v, want %s", err, c.code)
				}
				if strings.Contains(err.Error(), sid) {
					t.Fatalf("the error printed the session id")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			eng, err := a.Engine(background())
			if err != nil {
				t.Fatal(err)
			}
			id, err := ids.ParseAny(idOf(t, out))
			if err != nil {
				t.Fatal(err)
			}
			_, r, err := eng.Get(background(), engine.Ref{ID: id})
			if err != nil {
				t.Fatal(err)
			}
			if got := r.Fields["body"].String(); got != c.want {
				t.Fatalf("stored %q, want %q", got, c.want)
			}
		})
	}
}

// TestSessionIDShortIgnored: ids under minSessionIDLen never match (they
// would match ordinary words), even when the text holds them.
func TestSessionIDShortIgnored(t *testing.T) {
	sink := &sessionSink{ids: []string{"abc", "1234567", ""}}
	s, err := newScanner(background(), map[store.Source]store.Backend{}, withToolSessions(sink))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range s.Scan("body", "abc 1234567 abcdefgh") {
		if f.RuleID == RuleSessionID {
			t.Fatalf("a short id matched at %d", f.Offset)
		}
	}
	sink.set("abcdefgh")
	if err = s.Refresh(background()); err != nil {
		t.Fatal(err)
	}
	got := 0
	for _, f := range s.Scan("body", "abcdefgh and abcdefgh\nabcdefgh") {
		if f.RuleID == RuleSessionID {
			got++
			if strings.Contains(f.Preview, "abcdefgh") {
				t.Fatal("preview shows the id")
			}
		}
	}
	if got != 3 {
		t.Fatalf("%d hits, want every occurrence (3)", got)
	}
	// a sink without the capability adds no rule
	plain, err := newScanner(background(), map[store.Source]store.Backend{}, withToolSessions(nil))
	if err != nil {
		t.Fatal(err)
	}
	if plain.sessions != nil {
		t.Fatal("a nil sink must add no session source")
	}
}

// TestUseRepoGitleaksOff (D8, OV4): policy secrets.use_repo_gitleaks:
// false stops the repo .gitleaks.toml allow from letting a value through;
// turning it back on lets it through again (the scanner rebuilds on the
// switch, no reopen).
func TestUseRepoGitleaksOff(t *testing.T) {
	key := "AKIA" + strings.Repeat("Q", 16)
	repo := gitRepo(t)
	file := "[[allowlists]]\nregexes = ['''^AKIAQQQQ''']\n"
	if err := os.WriteFile(filepath.Join(repo, repoGitleaksFile), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, repo)
	withProject(t, a)
	addPolicy(t, a, policy.KeyOnDetect, `"refuse"`)
	if _, err := run(t, a, opsmemory.OpAdd, memBody(t, "deploy with "+key), false); err != nil {
		t.Fatalf("the repo allow must apply by default: %v", err)
	}
	addPolicy(t, a, policy.KeyUseRepoGitleaks, `false`)
	if _, err := run(t, a, opsmemory.OpAdd, memBody(t, "deploy again with "+key), false); !errs.Has(err, errs.SecretDetected) {
		t.Fatalf("use_repo_gitleaks false must ignore the repo allow: %v", err)
	}
}

// TestProseFilterPolicyOff (SECRETS 3.4): policy
// secrets.password_pair_prose_filter: false makes a write whose
// password_pair value is a plain word followed by prose a finding.
func TestProseFilterPolicyOff(t *testing.T) {
	a := newTestApp(t, gitRepo(t))
	withProject(t, a)
	addPolicy(t, a, policy.KeyOnDetect, `"refuse"`)
	text := "password = " + "tractor; restart api"
	if _, err := run(t, a, opsmemory.OpAdd, memBody(t, text), false); err != nil {
		t.Fatalf("default filter must let prose through: %v", err)
	}
	addPolicy(t, a, policy.KeyPasswordPairProseFilter, `false`)
	if _, err := run(t, a, opsmemory.OpAdd, memBody(t, text+" now"), false); !errs.Has(err, errs.SecretDetected) {
		t.Fatalf("filter off must refuse: %v", err)
	}
	if _, err := run(t, a, opsmemory.OpAdd, memBody(t, "the password: changeme"), false); err != nil {
		t.Fatalf("a placeholder is no finding even with the filter off: %v", err)
	}
}

// TestSessionIDReadErrorFailsWritesOnly (FB-SESS-2): when the tool session
// ids cannot be read (a damaged lore.db), the App still opens and reads
// the shared rows, while every write fails closed until the read works.
func TestSessionIDReadErrorFailsWritesOnly(t *testing.T) {
	sink := &sessionSink{}
	sink.fail(errors.New("file is not a database"))
	a := newSessionApp(t, gitRepo(t), sink)
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatalf("open with an unreadable session source: %v", err)
	}
	if _, lerr := run(t, a, opsmemory.OpList, `{}`, false); lerr != nil {
		t.Fatalf("a read must not need the session ids: %v", lerr)
	}
	name, err := value.TextOf("demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Create(background(), store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": name}); !errs.Has(err, errs.Internal) {
		t.Fatalf("write with unreadable session ids: err = %v, want %s", err, errs.Internal)
	}
	sink.fail(nil)
	withProject(t, a)
	if _, err := run(t, a, opsmemory.OpAdd, memBody(t, "a plain note"), false); err != nil {
		t.Fatalf("write after the session ids read again: %v", err)
	}
}
