package wire

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
)

// TestDirectPathSeesPersonOnly (devco D-877, bug hunt H-136): the roster
// verbs, task next / heartbeat / hold on an org store, actor merge outside
// a repo and sync from the setup run on App.run's direct path (Entry.Invoke,
// no engine.Run), where agents.person_only was never read: an agent
// registered a bot although the policy listed actor.register for people
// only, in a repo with a lore project and from a plain folder alike. The
// direct call now carries the check to every store it opens; a person
// passes; the refusal holds on a second open of the same state (an op that
// ignores the first error and opens again).
func TestDirectPathSeesPersonOnly(t *testing.T) {
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_REMOTE_SESSION_ID", "CODEX_THREAD_ID", "LORE_SESSION", "LORE_NO_DETECT"} {
		t.Setenv(k, "")
	}
	const register = `{"kind":"cron","label":"nightly"}`
	open := func(t *testing.T, dir string) *App {
		t.Helper()
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("policies:\n  agents.person_only: [actor.register]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		hook := func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
		}
		a, err := New(Options{RepoDir: dir, Home: home, OrgStores: hook, Wall: time.Now,
			Org: func(context.Context) (*identity.Org, error) {
				return &identity.Org{Name: "agency", Identity: identity.Entry{Key: testEmail, Handle: "alice"}}, nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	for _, place := range []struct {
		name string
		app  func(t *testing.T) *App
	}{
		{"plain-folder", func(t *testing.T) *App { return open(t, t.TempDir()) }},
		{"repo-with-a-lore-project", func(t *testing.T) *App {
			a := open(t, gitRepo(t))
			withProject(t, a)
			return a
		}},
	} {
		t.Run(place.name+"/agent", func(t *testing.T) {
			t.Setenv("LORE_HUMAN", "")
			t.Setenv("LORE_ACTOR", "agent:bot")
			a := place.app(t)
			_, err := run(t, a, opsinfra.OpActorRegister, register, true)
			if !errs.Has(err, errs.PolicyDenied) || !strings.Contains(err.Error(), "agents.person_only lists actor.register") {
				t.Fatalf("[devco:D-877] an agent's actor register listed in agents.person_only from a %s: %v, want the agents.person_only refusal", place.name, err)
			}
		})
		t.Run(place.name+"/person", func(t *testing.T) {
			t.Setenv("LORE_HUMAN", "1")
			t.Setenv("LORE_ACTOR", "")
			a := place.app(t)
			if _, err := run(t, a, opsinfra.OpActorRegister, `{}`, true); err != nil { // the person joins the roster
				t.Fatalf("a person's own actor register from a %s: %v", place.name, err)
			}
			if _, err := run(t, a, opsinfra.OpActorRegister, register, true); err != nil {
				t.Fatalf("a person's actor register from a %s: %v", place.name, err)
			}
		})
	}
	t.Run("refusal-kept-per-state", func(t *testing.T) {
		t.Setenv("LORE_HUMAN", "")
		t.Setenv("LORE_ACTOR", "agent:bot")
		a := open(t, gitRepo(t))
		withProject(t, a)
		e, ok := a.Registry().Lookup(opsinfra.OpActorRegister)
		if !ok {
			t.Fatal("no op actor.register")
		}
		ctx := a.directContext(background(), e, op.JSONDecoder{Body: []byte(register)}, op.Caller{})
		for i := range 2 {
			if _, err := a.state(ctx); !errs.Has(err, errs.PolicyDenied) {
				t.Fatalf("[devco:D-877] open %d of the direct call's state: %v, want E_POLICY_DENIED every time", i+1, err)
			}
		}
		if _, err := a.state(withoutDirectCall(ctx)); err != nil {
			t.Fatalf("the lookup open (withoutDirectCall): %v, want no check", err)
		}
	})
}

// directRunSites are the files of wire that call runDirect themselves,
// with why the call is covered. App.run's direct paths use runDirectAs.
var directRunSites = map[string]string{
	"roster_ops.go":           "runDirect itself",
	"directpolicy.go":         "runDirectAs: the funnel",
	"setupidentity_roster.go": "a setup step's own edit inside a running command: its ctx carries that command's direct call",
	"search_roster.go":        "search's roster read inside a running command: its ctx carries that command's direct call",
	"hostbase_doctor.go":      "doctor's own probe inside a running command: its ctx carries that command's direct call",
}

// TestDirectRunsCheckPersonOnly (devco D-877): a direct path of App.run
// that called runDirect with no directContext ran its operation with
// agents.person_only unread. In wire.go every runDirect call must sit
// behind directContext (the hostRunOp branch, which arms it before the
// roster opens), every other one goes through runDirectAs; other files
// are listed in directRunSites.
func TestDirectRunsCheckPersonOnly(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	call := regexp.MustCompile(`\brunDirect\(`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		if _, ok := directRunSites[f]; ok {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if !call.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if f == "wire.go" && strings.Contains(line, "armed above (directContext)") {
				continue
			}
			t.Errorf("[devco:D-877] %s:%d calls runDirect with agents.person_only unread: use a.runDirectAs (directpolicy.go), or add the file to directRunSites with why its ctx carries the check", f, i+1)
		}
	}
}
