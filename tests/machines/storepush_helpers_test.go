//go:build machines && !windows

package machines

// storepush_helpers_test.go — what the store push mode tests
// (storepush_test.go, life_storepush_test.go) share: git stores on a
// GitHub url that git rewrites to a bare repo on this disk (a second
// rewrite beside fake_gitserver.go's team store, for the personal store),
// the remote's history read with git, the push settings as `lore store
// show <store> --json` and `lore sync status --json` print them, and a
// lore binary built with the loretest tag (it honours LORE_NOW, so a test
// can make history older than a compaction cutoff).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// spMeURL is the personal store's url as its owner knows it.
const spMeURL = "git@github.com:alpha-dev/lore-me.git"

// spBare makes a bare repo under the machine's dir and points git's url
// for url at it (url.insteadOf in the machine's ~/.gitconfig).
func spBare(t *testing.T, m *Machine, name, url string) string {
	t.Helper()
	bare := filepath.Join(m.Dir, name+".git")
	if out, err := exec.Command("/usr/bin/git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v %s", err, out)
	}
	spRewrite(t, m, url, bare)
	return bare
}

// spRewrite points m's git at bare for url.
func spRewrite(t *testing.T, m *Machine, url, bare string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(m.Home, ".gitconfig"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("[url \"file://" + bare + "\"]\n\tinsteadOf = " + url + "\n"); err != nil {
		t.Fatal(err)
	}
}

// spCommit is one commit of a remote's main branch.
type spCommit struct {
	SHA     string
	Parents []string
	Date    string // committer date, YYYY-MM-DD
	Subject string
	Body    string
}

// spLog is the remote's main history, newest first.
func spLog(t *testing.T, bare string) []spCommit {
	t.Helper()
	const sep, end = "\x1f", "\x1e"
	out, err := exec.Command("/usr/bin/git", "--git-dir", bare, "log", "--format=%H"+sep+"%P"+sep+"%cs"+sep+"%s"+sep+"%b"+end, "main").CombinedOutput()
	if err != nil {
		t.Fatalf("git log %s: %v %s", bare, err, out)
	}
	var cs []spCommit
	for _, rec := range strings.Split(string(out), end) {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, sep, 5)
		if len(f) != 5 {
			t.Fatalf("git log record %q", rec)
		}
		cs = append(cs, spCommit{SHA: f[0], Parents: strings.Fields(f[1]), Date: f[2], Subject: f[3], Body: f[4]})
	}
	return cs
}

// spSubjects is each commit's subject, newest first.
func spSubjects(cs []spCommit) []string {
	var s []string
	for _, c := range cs {
		s = append(s, c.Subject)
	}
	return s
}

// spPush is a store's push settings as store show / store set print them
// (opsstores.PushView).
type spPush struct {
	Store        string `json:"store"`
	Place        string `json:"place"`
	Org          string `json:"org"`
	Push         string `json:"push"`
	PushEvery    string `json:"push_every"`
	Compact      string `json:"compact"`
	CompactAfter string `json:"compact_after"`
	Summary      string `json:"summary"`
	Problem      string `json:"problem"`
	Epoch        int    `json:"epoch"`
	Unpushed     int    `json:"unpushed"`
	LastPush     string `json:"last_push"`
	Admin        bool   `json:"admin"`
	Changed      bool   `json:"changed"`
}

// spShowOut is `lore store show --store <store> --json` (and GET /v1/stores/<store>).
type spShowOut struct {
	Push spPush `json:"push"`
}

// spShow is `lore store show --store <store> --json` in cwd.
func spShow(t *testing.T, m *Machine, cwd, store string) spPush {
	t.Helper()
	out := lsRun(t, m, cwd, "store", "show", store)
	var o spShowOut
	if err := json.Unmarshal([]byte(out), &o); err != nil || o.Push.Store == "" {
		t.Fatalf("store show %s: %v: %s", store, err, out)
	}
	return o.Push
}

// spSet is `lore store set --store <store> --settings k=v… --json` in cwd.
func spSet(t *testing.T, m *Machine, cwd, store string, kv ...string) spPush {
	t.Helper()
	out := lsRun(t, m, cwd, append([]string{"store", "set", store}, kv...)...)
	var v spPush
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Store == "" {
		t.Fatalf("store set %s %v: %v: %s", store, kv, err, out)
	}
	return v
}

// spStatusPush is one store of `lore sync status --json`'s data.stores:
// the store's standing (Ahead = local commits waiting) with its push mode
// folded in (wire/storestanding.go).
type spStatusPush struct {
	Store   string `json:"store"`
	Push    string `json:"push"`
	Summary string `json:"push_summary"`
	Ahead   int    `json:"ahead"`
	Epoch   int    `json:"epoch"`
	Problem string `json:"push_problem"`
}

// spSyncStatus is `lore sync status --json`'s data.stores, by store.
func spSyncStatus(t *testing.T, m *Machine, cwd string) map[string]spStatusPush {
	t.Helper()
	out := lsRun(t, m, cwd, "sync", "status")
	var o struct {
		Data struct {
			Stores []spStatusPush `json:"stores"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("sync status: %v: %s", err, out)
	}
	by := map[string]spStatusPush{}
	for _, s := range o.Data.Stores {
		by[s.Store] = s
	}
	return by
}

// spWant compares the settings a view shows with the expected ones.
type spWant struct {
	Push, PushEvery, Compact, CompactAfter string
}

func (w spWant) check(t *testing.T, step string, v spPush) {
	t.Helper()
	got := spWant{v.Push, v.PushEvery, v.Compact, v.CompactAfter}
	if got != w {
		t.Errorf("[%s] %s shows %+v, want %+v (summary %q)", step, v.Store, got, w, v.Summary)
	}
	if v.Problem != "" {
		t.Errorf("[%s] %s names a problem: %s", step, v.Store, v.Problem)
	}
	if !strings.HasPrefix(v.Summary, "push "+w.Push) {
		t.Errorf("[%s] %s summary %q does not start with push %s", step, v.Store, v.Summary, w.Push)
	}
	if !strings.Contains(v.Summary, "compact "+w.Compact) {
		t.Errorf("[%s] %s summary %q does not name compact %s", step, v.Store, v.Summary, w.Compact)
	}
}

// spMemAdd adds a memory to store in cwd and returns its id.
func spMemAdd(t *testing.T, m *Machine, cwd, store, body string) string {
	t.Helper()
	out := lsRun(t, m, cwd, "memory", "add", "--body", body, "--to", store)
	var mem struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(out), &mem) != nil || mem.ID == "" {
		t.Fatalf("memory add printed no id: %s", out)
	}
	return mem.ID
}

// The loretest binary: built once per test process (the Taskfile builds
// MACHINES_LORE without the tag). MACHINES_LORE_TEST names a prebuilt one.
const envLoreTest = "MACHINES_LORE_TEST"

var (
	spTestBinOnce sync.Once
	spTestBinPath string
	spTestBinErr  string
)

// spTestBin is a lore built with -tags loretest (LORE_NOW sets its clock).
func spTestBin(t *testing.T) string {
	t.Helper()
	spTestBinOnce.Do(func() {
		if p := os.Getenv(envLoreTest); p != "" {
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
			spTestBinPath = p
			return
		}
		// tmp/bin is the repo's gitignored build folder.
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			spTestBinErr = err.Error()
			return
		}
		p := filepath.Join(root, "tmp", "bin", "lore-machines-loretest")
		c := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-tags", "loretest", "-o", p, "./cmd/lore")
		c.Dir = root
		c.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := c.CombinedOutput(); err != nil {
			spTestBinErr = err.Error() + "\n" + string(out)
			return
		}
		spTestBinPath = p
	})
	if spTestBinPath == "" {
		t.Fatalf("cannot build lore with -tags loretest: %s", spTestBinErr)
	}
	return spTestBinPath
}

// spAt sets the clock of a loretest machine (LORE_NOW, RFC 3339).
func spAt(m *Machine, now string) { spEnv(m, "LORE_NOW", now) }

// spEnv sets KEY=value in m's extra environment, replacing an earlier
// one; value "" removes it.
func spEnv(m *Machine, key, value string) {
	var env []string
	for _, kv := range m.ExtraEnv {
		if !strings.HasPrefix(kv, key+"=") {
			env = append(env, kv)
		}
	}
	if value != "" {
		env = append(env, key+"="+value)
	}
	m.ExtraEnv = env
}
