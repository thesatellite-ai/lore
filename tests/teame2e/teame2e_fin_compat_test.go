//go:build teame2e

package teame2e

// TASK_SYSTEM_DESIGN 6.15 ("a v0.1.17 binary reading a row with parent_id
// … skips on the branch path (known gap, R9)") and 14.5 ("old-binary read of
// assigned_to_actor_id = lead") with the real v0.1.17 binary, built from the
// tag. The designed behaviour (FORMAT_EVOLUTION_DESIGN "Known gap
// (accepted)", R9; TASK_SYSTEM_DESIGN 23 "Format"): every row a 0.1.18 lore
// writes is `_v: 2`; v0.1.17 does not read those files on the branch path
// and tells the person to upgrade, so it sees a stale view, but it never
// rewrites them, and a 0.1.18 lore afterwards still reads parent_id, the
// lead and the assignee list unchanged.
//
// The live half of 6.15 (v0.1.17 goes read-only on the live path once a
// live row carries parent_id) is not here: it fails today, see the
// fin-compat report.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
	"github.com/thesatellite-ai/lore/tests/tmpworld"
)

const (
	// envOldBin names a prebuilt v0.1.17 binary; unset, the tests build
	// one from the tag.
	envOldBin = "LORE_OLD_BIN"
	// oldLoreTag is the last release before the 0.1.18 format.
	oldLoreTag = "v0.1.17"
	// oldLoreMain is v0.1.17's release main package (its .goreleaser.yml
	// builds.main), built inside the tag's go.work.
	oldLoreMain = "./saas/cmd/cli"
	// oldNewerFormat is what v0.1.17 says for a `_v: 2` file
	// (lsync decodeDoc): the person is told to upgrade.
	oldNewerFormat = "file written by a newer lore format"
	oldUpgrade     = "upgrade lore"
	// oldLiveFloor is v0.1.17's refusal of any write verb once the live
	// ref's min_lore_version is above it (its live_flags.go command gate).
	oldLiveFloor = "this lore is too old to write live rows here"
)

// The v0.1.17 build is shared by every test of this process (one build of
// a fixed tag, a few seconds with a warm module cache).
var (
	oldBinOnce sync.Once
	oldBinPath string
	oldBinErr  error
	// oldBinDir is the temp folder of the build (source export and
	// binary, ~1,500 files); runMain removes it when the suite ends. It
	// used to stay in the shared temp folder after every run.
	oldBinDir string
)

// removeOldLore deletes the v0.1.17 build folder (runMain, after the run).
func removeOldLore() {
	if oldBinDir != "" {
		_ = tmpworld.RemoveAll(oldBinDir) // best effort: a temp folder
	}
}

// oldLore returns the v0.1.17 binary: LORE_OLD_BIN, else built once from
// the tag. It never skips: a binary that cannot be had fails the test.
func oldLore(t *testing.T) string {
	t.Helper()
	if b := os.Getenv(envOldBin); b != "" {
		return b
	}
	oldBinOnce.Do(func() { oldBinPath, oldBinErr = buildOldLore() })
	if oldBinErr != nil {
		t.Fatalf("build the %s binary (or set %s to a prebuilt one): %v", oldLoreTag, envOldBin, oldBinErr)
	}
	return oldBinPath
}

// buildOldLore exports the tag with git archive into a temp dir (the
// checkout is not touched) and builds it the way its release did:
// CGO_ENABLED=0 with the tag's go.work active.
func buildOldLore() (string, error) {
	dir, err := os.MkdirTemp("", "lore-old-")
	if err != nil {
		return "", err
	}
	oldBinDir = dir
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		return "", err
	}
	// git archive from a subfolder archives only that subfolder, so it
	// runs at the top level of this checkout.
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", errors.New("find the repo top level: " + err.Error())
	}
	var arch, errb bytes.Buffer
	archive := exec.Command("git", "archive", "--format=tar", oldLoreTag)
	archive.Dir = string(bytes.TrimSpace(top))
	archive.Stdout, archive.Stderr = &arch, &errb
	if err := archive.Run(); err != nil {
		return "", errors.New("git archive " + oldLoreTag + " (is the tag fetched?): " + err.Error() + ": " + errb.String())
	}
	untar := exec.Command("tar", "-x", "-C", src)
	untar.Stdin = &arch
	if out, err := untar.CombinedOutput(); err != nil {
		return "", errors.New("untar: " + err.Error() + ": " + string(out))
	}
	bin := filepath.Join(dir, "lore-"+oldLoreTag)
	build := exec.Command("go", "build", "-trimpath", "-ldflags", "-X main.version="+strings.TrimPrefix(oldLoreTag, "v"), "-o", bin, oldLoreMain)
	build.Dir = src
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=", "GOWORK="+filepath.Join(src, "go.work"))
	if out, err := build.CombinedOutput(); err != nil {
		return "", errors.New("go build: " + err.Error() + ": " + string(out))
	}
	return bin, nil
}

// runOld runs the v0.1.17 binary in c with c's actor environment (HOME,
// PATH with the git wrapper, clock), exactly as Clone.Lore runs the
// binary under test.
func runOld(t *testing.T, c *th.Clone, args ...string) th.Result {
	t.Helper()
	bin := oldLore(t)
	cmd := c.Command(args...)
	cmd.Path, cmd.Args[0], cmd.Err = bin, bin, nil
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("start %s %v: %v", oldLoreTag, args, err)
		}
		code = ee.ExitCode()
	}
	return th.Result{Args: args, Stdout: out.String(), Stderr: errb.String(), Code: code}
}

// compatTaskView is the part of `lore task show --json` (0.1.18) these
// tests read.
type compatTaskView struct {
	Data struct {
		ID        string   `json:"id"`
		Parent    string   `json:"parent_id"`
		Lead      string   `json:"lead"`
		Assignees []string `json:"assignees"`
		Priority  string   `json:"priority"`
	} `json:"data"`
}

// compatShow runs the 0.1.18 `task show --json` on c.
func compatShow(t *testing.T, c *th.Clone, id string) compatTaskView {
	t.Helper()
	return th.DecodeJSON[compatTaskView](t, c.MustLore("task", "show", id, "--json"))
}

// compatRowFiles reads the branch row files of ids in c (path → bytes);
// every one must exist.
func compatRowFiles(t *testing.T, c *th.Clone, ids ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, id := range ids {
		p := mainRowPath("", tblTasks, id)
		b, ok := c.ReadFile(p)
		if !ok {
			t.Fatalf("%s: no %s", c.Dir, p)
		}
		out[p] = b
	}
	return out
}

// requireFilesUnchanged fails when any file of before differs in c now.
func requireFilesUnchanged(t *testing.T, c *th.Clone, before map[string]string, after string) {
	t.Helper()
	for p, b := range before {
		now, ok := c.ReadFile(p)
		if !ok || now != b {
			t.Fatalf("%s rewrote or removed %s (%s):\nbefore %s\nnow    %s", oldLoreTag, p, after, b, now)
		}
	}
}

// requireTreeAndLead checks, with the 0.1.18 binary, that child is under
// parent and leadTask has lead as its lead and only assignee.
func requireTreeAndLead(t *testing.T, c *th.Clone, parent, child, leadTask, lead string) {
	t.Helper()
	if v := compatShow(t, c, child); v.Data.Parent != parent {
		t.Fatalf("0.1.18 reads %s with parent_id %q, want %s", child, v.Data.Parent, parent)
	}
	v := compatShow(t, c, leadTask)
	if v.Data.Lead != lead || strings.Join(v.Data.Assignees, ",") != lead {
		t.Fatalf("0.1.18 reads %s with lead %q assignees %v, want lead %s and only assignee", leadTask, v.Data.Lead, v.Data.Assignees, lead)
	}
}

// TestFinCompat_V017FreshCloneRefusesTreeRows: a v0.1.17 binary in a new
// clone of a repo whose rows a 0.1.18 lore wrote (a parent, a child with
// parent_id, a task with a lead) reads nothing: `.lore/data/_meta.json` is
// `_v: 2` too, so every command stops with the upgrade message, writes no
// file, and the 0.1.18 rows on origin keep parent_id and the lead.
func TestFinCompat_V017FreshCloneRefusesTreeRows(t *testing.T) {
	rt := newRepoTeam(t, th.Flag("parent", "task", "add"), th.Flag("lead", "task", "edit"), th.Flag("no-live", "task", "add"))
	oldLore(t) // build before any clone, so a build failure is reported first
	rt.c1.MustLore("actor", "register")
	d1 := actorIDOf(t, rt.c1, "dev1")
	p := rt.addTask(t, rt.c1, "parent", "--no-live")
	ch := rt.addTask(t, rt.c1, "child", "--parent", p, "--no-live")
	l := rt.addTask(t, rt.c1, "lead", "--no-live")
	rt.c1.MustLore("task", "edit", l, "--assign", d1, "--lead", d1, "--no-live")
	rt.c1.CommitAll("tasks")
	rt.c1.Git("pull", "-q", "--rebase", "origin", "main")
	rt.c1.AdminPush("origin", "main")
	if m, _ := rt.c1.ReadFile(".lore/data/_meta.json"); !strings.Contains(m, "task_tree") {
		t.Fatalf("the first parent_id write declares task_tree in _meta.json (TASK_SYSTEM 23):\n%s", m)
	}
	onMain := map[string]string{}
	for _, id := range []string{p, ch, l} {
		onMain[id], _ = rt.app.Read(refMain, mainRowPath("", tblTasks, id))
	}

	old := rt.w.Human("old", "old@acme.example")
	old.WriteConfig(th.Config{Live: th.FreshFetch()})
	co := old.Clone(rt.app, "app")
	before := compatRowFiles(t, co, p, ch, l)
	// Writes stop first at the live floor: dev1's `actor register` put its
	// actor row on the live ref, which raised _live/meta.json to 0.1.18
	// (FORMAT_EVOLUTION rule 2, F-COMPAT-LIVE-1), and v0.1.17 checks that
	// floor before any write verb.
	writes := map[string]bool{"edit": true, "add": true}
	for _, args := range [][]string{
		{"task", "list", "--json"},
		{"task", "show", ch, "--json"},
		{"task", "show", l, "--json"},
		{"task", "edit", ch, "--priority", "high", "--no-live"},
		{"task", "add", "old one", "--tasklist", rt.tasklist, "--no-live"},
		{"sync"},
	} {
		r := runOld(t, co, args...)
		say := oldNewerFormat
		if len(args) > 1 && writes[args[1]] {
			say = oldLiveFloor
		}
		if r.Code == 0 || !strings.Contains(r.Out(), say) || !strings.Contains(r.Out(), oldUpgrade) {
			t.Fatalf("%s %v in a fresh clone of a 0.1.18 repo stops and says %q / %q:\n%s", oldLoreTag, args, say, oldUpgrade, r)
		}
		if strings.Contains(r.Stdout, p) || strings.Contains(r.Stdout, l) {
			t.Fatalf("%s %v printed a 0.1.18 row it cannot read:\n%s", oldLoreTag, args, r)
		}
	}
	if st := co.Status(); strings.Contains(st, ".lore/data") {
		t.Fatalf("%s changed .lore/data in a clone it refused:\n%s", oldLoreTag, st)
	}
	requireFilesUnchanged(t, co, before, "fresh clone")
	for id, b := range onMain {
		if now, _ := rt.app.Read(refMain, mainRowPath("", tblTasks, id)); now != b {
			t.Fatalf("origin main row %s changed after %s ran", id, oldLoreTag)
		}
	}
	rt.c1.Git("pull", "-q", "--rebase", "origin", "main")
	requireTreeAndLead(t, rt.c1, p, ch, l, d1)
}

// TestFinCompat_V017ExistingCloneSkipsTreeRows: a teammate who used the
// repo on v0.1.17 before it moved to 0.1.18 (`lore migrate run`) pulls the
// tree and lead rows. v0.1.17 skips every `_v: 2` file with the upgrade
// message (stale view: the child and the lead task are not found), its own
// edits never rewrite those files (also not a v0.1.17 row the 0.1.18 lore
// re-parented and gave a lead), and after it commits and pushes its own
// work the 0.1.18 lore still reads parent_id, the lead and the assignees.
func TestFinCompat_V017ExistingCloneSkipsTreeRows(t *testing.T) {
	w := newTeamWorld(t, th.Flag("parent", "task", "add"), th.Flag("lead", "task", "edit"), th.Flag("no-live", "task", "add"), th.Cmd("migrate", "run"))
	oldLore(t)
	app := w.Remote(slugApp)
	dev1 := w.Human("dev1", "dev1@acme.example")
	old := w.Human("old", "old@acme.example")
	for _, a := range []*th.Actor{dev1, old} {
		a.WriteConfig(th.Config{Live: th.FreshFetch()})
	}
	mustOld := func(c *th.Clone, args ...string) th.Result {
		t.Helper()
		r := runOld(t, c, args...)
		if r.Code != 0 {
			t.Fatalf("%s %v: %s", oldLoreTag, args, r)
		}
		return r
	}

	// The repo starts on v0.1.17: one task, committed.
	co := old.Clone(app, "app")
	mustOld(co, "init", "--non-interactive", "--name=app")
	tl := mustID(t, mustOld(co, "tasklist", "add", "--title", "Inbox", "--body", "default list"), "tlt")
	legacy := mustID(t, mustOld(co, "task", "add", "legacy", "--tasklist", tl, "--no-live"), "tsk")
	co.CommitAll("lore on v0.1.17")
	co.AdminPush("origin", "main")

	// dev1 moves it to 0.1.18 and builds a tree with a lead on the branch.
	c1 := dev1.Clone(app, "app")
	c1.MustLore("migrate", "run")
	c1.MustLore("actor", "register")
	d1 := actorIDOf(t, c1, "dev1")
	p := mustID(t, c1.MustLore("task", "add", "parent", "--tasklist", tl, "--no-live"), "tsk")
	ch := mustID(t, c1.MustLore("task", "add", "child", "--tasklist", tl, "--parent", p, "--no-live"), "tsk")
	l := mustID(t, c1.MustLore("task", "add", "lead", "--tasklist", tl, "--no-live"), "tsk")
	c1.MustLore("task", "edit", l, "--assign", d1, "--lead", d1, "--no-live")
	c1.MustLore("task", "edit", legacy, "--parent", p, "--assign", d1, "--lead", d1, "--no-live")
	c1.CommitAll("tree and lead on 0.1.18")
	c1.Git("pull", "-q", "--rebase", "origin", "main")
	c1.AdminPush("origin", "main")

	// The v0.1.17 clone pulls. Its own .gitignore line is dropped first
	// (v0.1.17 appends one on every run; it is not what this test is about).
	co.Git("checkout", "--", ".gitignore")
	co.Git("pull", "-q", "--rebase", "origin", "main")
	before := compatRowFiles(t, co, legacy, p, ch, l)

	r := mustOld(co, "task", "list", "--json")
	for _, id := range []string{p, ch, l} {
		if strings.Contains(r.Stdout, id) {
			t.Fatalf("%s lists the 0.1.18 row %s it cannot read:\n%s", oldLoreTag, id, r)
		}
		if !strings.Contains(r.Stderr, "tasks/"+id+".json") {
			t.Fatalf("%s names the skipped file of %s:\n%s", oldLoreTag, id, r)
		}
	}
	r.Says(t, oldNewerFormat, oldUpgrade)
	for _, id := range []string{ch, l} {
		runOld(t, co, "task", "show", id, "--json").Fails(t, th.ExitError, "E_NOT_FOUND", oldNewerFormat)
	}
	// Writes through v0.1.17: an edit of a row it holds from before (now a
	// child with a lead on 0.1.18) and of a row it cannot read, and a new
	// task of its own, all stop with E_OLD_LORE: dev1's `actor register`
	// put its actor row on the live ref, which raised _live/meta.json to
	// 0.1.18 (FORMAT_EVOLUTION rule 2, F-COMPAT-LIVE-1), and v0.1.17 checks
	// that floor before every write verb. A sync still runs.
	for _, args := range [][]string{
		{"task", "edit", legacy, "--priority", "high", "--no-live"},
		{"task", "edit", ch, "--priority", "high", "--no-live"},
		{"task", "add", "old one", "--tasklist", tl, "--no-live"},
	} {
		runOld(t, co, args...).Fails(t, th.ExitGuard, "E_OLD_LORE", oldLiveFloor, oldUpgrade)
	}
	runOld(t, co, "sync").Says(t, oldNewerFormat)
	requireFilesUnchanged(t, co, before, "after its edits and sync")

	co.Git("checkout", "--", ".gitignore")
	co.CommitAll("old lore work")
	co.Git("pull", "-q", "--rebase", "origin", "main")
	co.AdminPush("origin", "main")
	c1.Git("pull", "-q", "--rebase", "origin", "main")
	requireTreeAndLead(t, c1, p, ch, l, d1)
	requireTreeAndLead(t, c1, p, legacy, legacy, d1)
	if v := compatShow(t, c1, legacy); v.Data.Priority == "high" {
		t.Fatalf("a v0.1.17 edit of a `_v: 2` row reached the file: %+v", v.Data)
	}
}
