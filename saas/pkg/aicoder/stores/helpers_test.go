package stores

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"saas/pkg/aicoder/clock"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/gitremote"
	"saas/pkg/aicoder/gitremote/gitremotetest"
)

// fakeHost is the host every test store URL names; a global gitconfig
// rewrites it to bare repos in a temp folder (url.insteadOf), so lore sees
// real-looking URLs exactly like the e2e harness does.
const fakeHost = "example.test"

var sig = gitref.Signature{Name: "Org Identity", Email: "alice@northwind.example", When: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)}

// env is one test world: a folder of bare "hosted" repos and a global git
// config pointing https://example.test/ at it.
type env struct {
	t     *testing.T
	hosts string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	hosts := filepath.Join(root, "hosts")
	if err := os.MkdirAll(hosts, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "gitconfig")
	body := "[url \"" + hosts + "/\"]\n\tinsteadOf = https://" + fakeHost + "/\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return &env{t: t, hosts: hosts}
}

// remote creates an empty bare repo reachable as https://example.test/<slug>.
func (e *env) remote(slug string) string {
	e.t.Helper()
	dir := filepath.Join(e.hosts, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	gitremotetest.Git(e.t, dir, "init", "-q", "--bare", "-b", "main")
	return "https://" + fakeHost + "/" + slug
}

func (e *env) remoteDir(slug string) string { return filepath.Join(e.hosts, slug) }

// seed commits files to branch of a hosted repo through a scratch clone.
func (e *env) seed(slug, branch string, files map[string]string) {
	e.t.Helper()
	wd := filepath.Join(e.t.TempDir(), "seed")
	gitremotetest.Git(e.t, filepath.Dir(wd), "clone", "-q", e.remoteDir(slug), wd)
	gitremotetest.Git(e.t, wd, "checkout", "-q", "-B", branch)
	for p, c := range files {
		full := filepath.Join(wd, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	gitremotetest.Git(e.t, wd, "add", "-A")
	gitremotetest.Git(e.t, wd, "commit", "-q", "-m", "seed")
	gitremotetest.Git(e.t, wd, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
}

// manager is one machine's lore home.
func (e *env) manager(home string, fault *faults) *Manager {
	m := &Manager{Layout: Layout{Home: home}, Clock: clock.NewFake(sig.When)}
	if fault != nil {
		m.RemoteFor = func(r *gitref.Repo) gitremote.Remote {
			f := gitremote.NewFaultRemote(&gitremote.Git{Repo: r, Name: RemoteName})
			f.SetOffline(fault.offline)
			return f
		}
	}
	return m
}

// faults switches a manager's remotes offline.
type faults struct{ offline bool }

func ctx() context.Context { return context.Background() }

// originFile reads a file on a hosted repo's branch.
func (e *env) originFile(slug, ref, path string) (string, bool) {
	e.t.Helper()
	r, err := gitref.Open(ctx(), e.remoteDir(slug), nil)
	if err != nil {
		e.t.Fatal(err)
	}
	b, ok, err := r.ReadFile(ctx(), ref, path)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b), ok
}

func (e *env) originRev(slug, ref string) string {
	e.t.Helper()
	r, err := gitref.Open(ctx(), e.remoteDir(slug), nil)
	if err != nil {
		e.t.Fatal(err)
	}
	oid, _, err := r.ResolveRef(ctx(), ref)
	if err != nil {
		e.t.Fatal(err)
	}
	return oid
}

// newProjectFiles is a minimal project template for WriteLink.
func newProjectFiles(id string) func(string) (map[string][]byte, error) {
	return func(string) (map[string][]byte, error) {
		m := StoreMeta{ProjectID: id}
		b, err := m.Encode()
		if err != nil {
			return nil, err
		}
		return map[string][]byte{metaFileName: b}, nil
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// advanced is a fake clock d after start.
func advanced(start time.Time, d time.Duration) *clock.Fake { return clock.NewFake(start.Add(d)) }
