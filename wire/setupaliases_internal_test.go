package wire

// github.key_matches never waits on a slow alias, the per-alias answers
// are shared (cache + one probe in flight), setup.aliases reads them, the
// folder-store checks, and the prefill cache's "cached" trace call.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/wire/internal/fakebin"
)

// fakeSSH is an ssh that greets per alias or -i key after 0.1 s; the alias
// github-slow hangs (past aliasProbeTimeout). Every call is logged.
const fakeSSH = `echo "$*" >> "$HOME/ssh.log"
key=""; host=""; prev=""
for a in "$@"; do [ "$prev" = "-i" ] && key="$a"; prev="$a"; host="$a"; done
host="${host#git@}"
hi(){ sleep 0.1; echo "Hi $1! You've successfully authenticated, but GitHub does not provide shell access." >&2; exit 1; }
if [ -n "$key" ]; then case "$(basename "$key")" in work) hi octo-work;; *) hi octo-personal;; esac; fi
case "$host" in
  github.com|octo-p) hi octo-personal;;
  github-work) hi octo-work;;
  github-slow) sleep 10; exit 255;;
esac
echo "ssh: Could not resolve hostname $host" >&2; exit 255`

const fakeSSHConfig = `Host github.com
  IdentityFile ~/.ssh/work_key
Host octo-p
  HostName github.com
  IdentityFile ~/.ssh/octo-p
Host github-work
  HostName github.com
  IdentityFile ~/.ssh/work
Host github-slow
  HostName github.com
  IdentityFile ~/.ssh/slow
`

// aliasWorld is a home with that ssh config and keys, a fake ssh, and a
// setupProber over a fresh App cache.
func aliasWorld(t *testing.T) (setupProber, string) {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	ssh := filepath.Join(home, ".ssh")
	for _, d := range []string{bin, ssh} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fakebin.Write(t, filepath.Join(bin, "ssh"), "#!/bin/sh\n"+fakeSSH+"\n", 0o755)
	files := map[string]string{"config": fakeSSHConfig, "work_key": "k", "octo-p": "k", "octo-p.pub": "ssh-ed25519 AAAA pat", "work": "k", "work.pub": "ssh-ed25519 AAAA work", "slow": "k"}
	for n, body := range files {
		if err := os.WriteFile(filepath.Join(ssh, n), []byte(body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	look := func(name string) (string, error) {
		if p := filepath.Join(bin, name); name == "ssh" || name == "gh" {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
		return exec.LookPath(name)
	}
	p := probe.New(probe.Options{Home: home, TmpDir: t.TempDir(), LookPath: look,
		Env: []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home}})
	a := &App{aliasCache: newAliasCache(nil), prefillCache: newPrefillCache(nil)}
	// As the lore binary does at exit: Close ends the probes a check left
	// running, before TempDir removes the home their ssh writes to.
	t.Cleanup(func() { _ = a.Close() })
	return setupProber{a: a, p: p}, home
}

// aliasAnswerGuard bounds waitAliasAnswers: far past any probe's own time
// limit, so it only ends a test whose background probe never reports.
const aliasAnswerGuard = 2 * time.Minute

// waitAliasAnswers returns once the alias cache holds an answer for every
// alias in names (the background sweep a check started), failing with
// the ones still missing after aliasAnswerGuard.
func waitAliasAnswers(t *testing.T, sp setupProber, names ...string) {
	t.Helper()
	deadline := time.Now().Add(aliasAnswerGuard)
	for {
		var missing []string
		for _, n := range names {
			if _, ok := sp.a.aliasCache.getKey(aliasKey(n)); !ok {
				missing = append(missing, n)
			}
		}
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("deadlock guard: no background answer for %v after %v", missing, aliasAnswerGuard)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// sshCalls counts logged ssh runs whose arguments contain needle.
func sshCalls(t *testing.T, home, needle string) int {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(home, "ssh.log"))
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, needle) {
			n++
		}
	}
	return n
}

// keyReq is a key_matches request for a setup that records key over alias.
func keyReq(alias, key string) opssetup.ProbeRequest {
	return opssetup.ProbeRequest{Check: onboard.Check{ID: onboard.CheckGitHubKeyMatches, Level: onboard.LevelError},
		Setup: onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo,
			GitHub: onboard.SetupGitHub{User: "octo-personal", HostAlias: alias, SSHKey: key}}}
}

func TestKeyMatchesDoesNotWaitOnSlowAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a fake ssh")
	}
	sp, home := aliasWorld(t)
	ctx := context.Background()
	r := sp.keyMatches(ctx, keyReq("github.com", "~/.ssh/work.pub"))
	if r.Status != onboard.StatusFail {
		t.Fatalf("status = %s (%s)", r.Status, r.Detail)
	}
	// Cold: the check's own two probes only (github.com already logs in as
	// the account, so its runnable fix is used at once); never a wait for
	// the other aliases, the slow one least of all. Told by order, not by
	// a clock: github-slow never answers before its probe's own time
	// limit, so had the check waited for it, its answer (a timeout) would
	// be in the cache now.
	if res, done := sp.a.aliasCache.getKey(aliasKey("github-slow")); done {
		t.Errorf("the check returned after github-slow answered (%s): it waited for the slow alias", res.Class)
	}
	if r.Vars[onboard.VarFixAlias] != probe.GitHubHost || r.Vars[onboard.VarFixVariant] != string(onboard.FixKeyAliasOnly) ||
		!strings.Contains(r.Detail, "still asking") {
		t.Errorf("cold = %+v", r)
	}
	// The other aliases answer in the background: wait for those answers
	// themselves (a deadlock guard only bounds it).
	waitAliasAnswers(t, sp, "octo-p", "github-work")

	// A re-run answers from the per-alias cache and now names the named
	// alias that logs in as the account, with its key; no new ssh for the
	// answered aliases, and the slow alias's probe is shared, not doubled.
	before := sshCalls(t, home, "github-work")
	r2 := sp.keyMatches(ctx, keyReq("github.com", "~/.ssh/work.pub"))
	if r2.Vars[onboard.VarFixKey] != "~/.ssh/octo-p.pub" || r2.Vars[onboard.VarFixVariant] != string(onboard.FixKeyPair) ||
		strings.Contains(r2.Detail, "github-slow") {
		t.Errorf("warm = %+v", r2)
	}
	if r2.Status != onboard.StatusFail || r2.Vars[onboard.VarFixAlias] != "octo-p" {
		t.Errorf("re-run = %+v", r2)
	}
	if n := sshCalls(t, home, "github-work"); n != before {
		t.Errorf("github-work probed again: %d → %d", before, n)
	}
	if n := sshCalls(t, home, "github-slow"); n != 1 {
		t.Errorf("github-slow probed %d times, want 1 (one in flight)", n)
	}
	// setup.aliases reads the same answers (no new probe of answered aliases).
	tbl, err := sp.GitHubAliases(ctx, opssetup.AliasesIn{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, a := range tbl.Aliases {
		got[a.Alias] = a.LogsInAs + "/" + a.Status
	}
	if got["octo-p"] != "octo-personal/ok" || got["github-work"] != "octo-work/ok" || got["github-slow"] != "/timeout" {
		t.Errorf("table = %v", got)
	}
	if n := sshCalls(t, home, "github-work"); n != before {
		t.Errorf("setup.aliases probed github-work again: %d → %d", before, n)
	}
}

func TestKeyMatchesNamesSilentAliases(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a fake ssh")
	}
	sp, _ := aliasWorld(t)
	// Only github.com logs in as the account (octo-p removed): the check
	// answers with github.com at once and names the aliases not heard from.
	cfg := strings.Replace(fakeSSHConfig, "Host octo-p\n  HostName github.com\n  IdentityFile ~/.ssh/octo-p\n", "", 1)
	if err := os.WriteFile(filepath.Join(sp.p.Home(), ".ssh", "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r := sp.keyMatches(context.Background(), keyReq("github.com", "~/.ssh/work.pub"))
	if res, done := sp.a.aliasCache.getKey(aliasKey("github-slow")); done {
		t.Errorf("returned after github-slow answered (%s): it waited for the slow alias", res.Class)
	}
	if r.Vars[onboard.VarFixAlias] != probe.GitHubHost || r.Vars[onboard.VarFixVariant] != string(onboard.FixKeyAliasOnly) {
		t.Errorf("fix vars = %v", r.Vars)
	}
	if !strings.Contains(r.Detail, "still asking") || !strings.Contains(r.Detail, "github-slow") {
		t.Errorf("detail does not name the silent alias: %s", r.Detail)
	}
}

func TestSilentNoteAndFixAlias(t *testing.T) {
	ok := func(u string) probe.SSHAuthResult {
		return probe.SSHAuthResult{Outcome: probe.Outcome{Class: probe.ClassOK}, User: u}
	}
	to := probe.SSHAuthResult{Outcome: probe.Outcome{Class: probe.ClassTimeout, Took: aliasProbeTimeout}}
	early := probe.SSHAuthResult{Outcome: probe.Outcome{Class: probe.ClassTimeout, Took: time.Millisecond}}
	order := []string{"github.com", "a", "b", "c"}
	cases := []struct {
		name     string
		got      map[string]probe.SSHAuthResult
		pending  []string
		fix      string
		noteHas  []string
		noteNone bool
	}{
		{"named before github.com", map[string]probe.SSHAuthResult{"github.com": ok("Octo-Personal"), "a": ok("octo-work"), "b": ok("octo-personal"), "c": ok("octo-personal")}, nil, "b", nil, true},
		{"github.com only", map[string]probe.SSHAuthResult{"github.com": ok("octo-personal"), "a": to}, []string{"b", "c"}, "github.com",
			[]string{"a did not answer in 3s", "still asking b, c"}, false},
		{"none", map[string]probe.SSHAuthResult{"github.com": ok("octo-work")}, nil, "", nil, true},
		// a probe that gave up early (its caller left) is "still asking", never "did not answer"
		{"early timeout", map[string]probe.SSHAuthResult{"github.com": ok("octo-personal"), "a": early, "b": to}, nil, "github.com",
			[]string{"b did not answer in 3s", "still asking a"}, false},
	}
	for _, c := range cases {
		fix, found := fixAlias(order, c.got, "octo-personal")
		if fix != c.fix || found != (c.fix != "") {
			t.Errorf("%s: fixAlias = %q %v", c.name, fix, found)
		}
		note := silentNote(order, c.got, c.pending)
		if c.noteNone != (note == "") {
			t.Errorf("%s: note %q", c.name, note)
		}
		for _, h := range c.noteHas {
			if !strings.Contains(note, h) {
				t.Errorf("%s: note %q lacks %q", c.name, note, h)
			}
		}
	}
}

// A timeout is remembered for aliasTimeoutTTL only; an answer for the
// full cache TTL.
func TestAliasCacheTimeoutTTL(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 6, 19, 0, 0, 0, time.UTC)
	c := newAliasCache(func() time.Time { return now })
	c.putKey("slow", probe.SSHAuthResult{Outcome: probe.Outcome{Class: probe.ClassTimeout}})
	c.putKey("fast", probe.SSHAuthResult{Outcome: probe.Outcome{Class: probe.ClassOK}, User: "octo-personal"})
	now = now.Add(aliasTimeoutTTL - time.Second)
	if _, ok := c.getKey("slow"); !ok {
		t.Error("timeout forgotten too early")
	}
	now = now.Add(2 * time.Second)
	if _, ok := c.getKey("slow"); ok {
		t.Error("timeout kept past aliasTimeoutTTL")
	}
	if _, ok := c.getKey("fast"); !ok {
		t.Error("answer forgotten before the cache TTL")
	}
}

// A file:// store that is a plain folder passes read and write (no git),
// its marker is the file itself, and privacy is skipped with a reason.
func TestFolderStoreChecksThroughProber(t *testing.T) {
	t.Parallel()
	sp, home := aliasWorld(t)
	dir := filepath.Join(home, "stores", "me")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ans := onboard.Answers{onboard.QIDStoreURL: {Text: "file://" + dir}}
	run := func(id onboard.CheckID) opssetup.ProbeResult {
		return sp.Probe(context.Background(), []opssetup.ProbeRequest{{Check: onboard.Check{ID: id, Level: onboard.LevelError}, Answers: ans}})[0]
	}
	if r := run(onboard.CheckStoreRead); r.Status != onboard.StatusOK || !strings.Contains(r.Detail, "folder store") {
		t.Errorf("store.read = %+v", r)
	}
	if r := run(onboard.CheckStoreWrite); r.Status != onboard.StatusOK {
		t.Errorf("store.write = %+v", r)
	}
	if r := run(onboard.CheckStoreMarker); r.Status != onboard.StatusFail || !strings.Contains(r.Detail, "no lore-store.yaml") {
		t.Errorf("store.marker = %+v", r)
	}
	if r := run(onboard.CheckStorePrivate); r.Status != onboard.StatusSkip || r.Detail != skipFolderStore {
		t.Errorf("store.private = %+v", r)
	}
	if r := run(onboard.CheckOrgFree); r.Status != onboard.StatusSkip {
		t.Errorf("org.free = %+v", r)
	}
}

// A prefill answered from the cache leaves a "cached" call under its
// label, so the step's Details say where the answer came from.
func TestPrefillCacheHitTraced(t *testing.T) {
	t.Parallel()
	sp, _ := aliasWorld(t)
	setup := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo}
	if _, err := sp.Prefill(context.Background(), onboard.PrefillNone, setup); err != nil {
		t.Fatal(err)
	}
	ctx, tr := probe.WithTrace(context.Background())
	if _, err := sp.Prefill(ctx, onboard.PrefillNone, setup); err != nil {
		t.Fatal(err)
	}
	calls := tr.Calls()
	if len(calls) != 1 || calls[0].Kind != probe.CallCache || calls[0].Label != "prefill "+string(onboard.PrefillNone) || calls[0].CachedAt.IsZero() {
		t.Fatalf("calls = %+v", calls)
	}
}

// Regression (repair, 2026-11-06): a step page opened first, its
// setup.aliases call dropped by the page while the probes ran. The shared
// per-alias probes ended with that request, every alias was kept as
// "timeout", and key_matches said all aliases "did not answer in 3s" with
// no runnable fix. Probes now outlive the caller; only full-length
// timeouts are kept or reported as such.
func TestKeyMatchesAfterDroppedAliasesCall(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a fake ssh")
	}
	sp, _ := aliasWorld(t)
	cfg := strings.Replace(fakeSSHConfig, "Host octo-p\n  HostName github.com\n  IdentityFile ~/.ssh/octo-p\n", "", 1)
	if err := os.WriteFile(filepath.Join(sp.p.Home(), ".ssh", "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	tctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = sp.GitHubAliases(tctx, opssetup.AliasesIn{})
	}()
	time.Sleep(30 * time.Millisecond) // the table's probes are running
	cancel()
	<-done
	if _, ok := sp.a.aliasCache.get(false); ok {
		t.Error("a table built under a dropped request was kept")
	}
	r := sp.keyMatches(context.Background(), keyReq("github.com", "~/.ssh/work.pub"))
	if r.Status != onboard.StatusFail || r.Vars[onboard.VarFixAlias] != probe.GitHubHost || r.Vars[onboard.VarFixVariant] != string(onboard.FixKeyAliasOnly) {
		t.Fatalf("key_matches = %+v", r)
	}
	if !strings.Contains(r.Detail, "which logs in as octo-personal") || strings.Contains(r.Detail, "github.com did not answer") ||
		strings.Contains(r.Detail, "github-work did not answer") {
		t.Errorf("detail reports timeouts that did not happen: %s", r.Detail)
	}
}

// The github step's validation reads the table as known now: file facts at
// once, ssh answers within the wait, a hung alias pending, never waited on.
func TestGitHubAliasesKnown(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a fake ssh")
	}
	sp, _ := aliasWorld(t)
	ctx := context.Background()
	// No wait: every alias is still pending (asserted below), which a
	// query that waited for any ssh answer could not report.
	out, pending, err := sp.GitHubAliasesKnown(ctx, opssetup.AliasQuery{})
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]opssetup.GitHubAlias{}
	for _, a := range out.Aliases {
		rows[a.Alias] = a
	}
	if len(pending) != len(out.Aliases) || rows["octo-p"].PubKey != "~/.ssh/octo-p.pub" || rows["octo-p"].Status != opssetup.StatusChecking {
		t.Errorf("cold rows = %+v pending %v", out.Aliases, pending)
	}
	// With a user: returns once an alias logs in as the user, the hung
	// alias still pending. The wait is far longer than github-slow's own
	// probe limit, so a query that sat out its wait would find github-slow
	// answered (timed out); returning at the first match finds it unheard.
	out, pending, err = sp.GitHubAliasesKnown(ctx, opssetup.AliasQuery{Wait: aliasAnswerGuard, User: "octo-personal"})
	if err != nil {
		t.Fatal(err)
	}
	if res, done := sp.a.aliasCache.getKey(aliasKey("github-slow")); done {
		t.Errorf("returned after github-slow answered (%s): it waited for the slow alias", res.Class)
	}
	if !slices.Contains(pending, "github-slow") {
		t.Errorf("github-slow not pending: %v", pending)
	}
	found := false
	for _, a := range out.Aliases {
		if a.LogsInAs == "octo-personal" {
			found = true
		}
		if a.Alias == "github-slow" && a.Status != opssetup.StatusChecking {
			t.Errorf("github-slow = %+v", a)
		}
	}
	if !found {
		t.Errorf("no alias logs in as octo-personal: %+v", out.Aliases)
	}
}

// Cold, and no alias known yet logs in as the account (the own alias logs
// in as someone else): the check waits for the others, within
// keyMatchNoFixWait, and names one that answers as the account.
func TestKeyMatchesWaitsWhenNoFixKnown(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a fake ssh")
	}
	sp, _ := aliasWorld(t)
	r := sp.keyMatches(context.Background(), keyReq("github-work", "~/.ssh/work.pub"))
	// Waited for the first answer as the account, not to keyMatchNoFixWait:
	// github-slow, whose probe limit is inside that wait, is still unheard.
	if res, done := sp.a.aliasCache.getKey(aliasKey("github-slow")); done {
		t.Errorf("returned after github-slow answered (%s): it waited for the slow alias", res.Class)
	}
	// The first alias heard as the account names the fix (octo-p or
	// github.com: both answer as octo-personal at the same time here).
	fix := r.Vars[onboard.VarFixAlias]
	if r.Status != onboard.StatusFail || (fix != "octo-p" && fix != probe.GitHubHost) || r.Vars[onboard.VarFixVariant] == "" {
		t.Fatalf("key_matches = %+v", r)
	}
}

// writeGH puts a fake gh in the world's bin.
func writeGH(t *testing.T, home, script string) {
	t.Helper()
	fakebin.Write(t, filepath.Join(home, "bin", "gh"), "#!/bin/sh\n"+script+"\n", 0o755)
}

// store.private when gh cannot answer: the visibility is unknown, so the
// check could not run (error, no fix), never "public" with a Run-it fix.
func TestStorePrivateGHFails(t *testing.T) {
	t.Parallel()
	sp, home := aliasWorld(t)
	ans := onboard.Answers{onboard.QIDStoreURL: {Text: "git@github.com:octo-personal/notes.git"}}
	req := opssetup.ProbeRequest{Check: onboard.Check{ID: onboard.CheckStorePrivate, Level: onboard.LevelWarn}, Answers: ans}
	cases := []struct {
		name, gh string
		status   onboard.Status
		has      string
	}{
		{"404", `echo "gh: Not Found (HTTP 404)" >&2; exit 1`, onboard.StatusError, "could not tell whether octo-personal/notes is private"},
		{"not logged in", `echo "To get started with GitHub CLI, please run:  gh auth login" >&2; exit 4`, onboard.StatusError, "could not tell"},
		{"public", `printf 'false\ntrue\n'`, onboard.StatusWarn, "is public"},
		{"private", `printf 'true\ntrue\n'`, onboard.StatusOK, ""},
	}
	for _, c := range cases {
		writeGH(t, home, c.gh)
		r := sp.Probe(context.Background(), []opssetup.ProbeRequest{req})[0]
		if r.Status != c.status || !strings.Contains(r.Detail, c.has) {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
}

// github.gh_matches is informational (lore runs gh as the setup's own
// login, so gh's active account only matters for gh by hand): over every
// active-account shape it is never a warning or a failure. No active
// account is a plain skip, never "gh's active account is ." with an empty
// name; another active account is ok and says so in the detail; the
// setup's own account (any case) is ok. The catalog level is info.
func TestGHMatchesInfoOnly(t *testing.T) {
	t.Parallel()
	sp, home := aliasWorld(t)
	c, ok := onboard.CheckByID(onboard.CheckGitHubGHMatches)
	if !ok || c.Level != onboard.LevelInfo {
		t.Fatalf("github.gh_matches catalog row %+v, want level info", c)
	}
	const user = "octo-personal"
	active := func(login string) string {
		return `printf 'github.com\n  ✓ Logged in to github.com account ` + login + ` (keyring)\n  - Active account: true\n' >&2`
	}
	cases := []struct {
		name, gh string
		status   onboard.Status
		has      string
	}{
		{"no active login", `printf 'github.com\n  X Failed to log in to github.com account octo-work (keyring)\n  - Active account: false\n' >&2; exit 1`, onboard.StatusSkip, "no active github.com account"},
		{"not logged in", `echo "You are not logged into any GitHub hosts. To log in, run: gh auth login" >&2; exit 1`, onboard.StatusSkip, "no active github.com account"},
		{"other active", active("octo-work"), onboard.StatusOK, "gh's active account is octo-work; lore runs gh as " + user},
		{"own account active", active(user), onboard.StatusOK, "gh's active account is " + user},
		{"own account, other case", active("Octo-Personal"), onboard.StatusOK, "gh's active account is Octo-Personal"},
	}
	for _, level := range []onboard.Level{c.Level, onboard.LevelWarn} { // a stale caller's warn level changes nothing
		req := opssetup.ProbeRequest{Check: onboard.Check{ID: onboard.CheckGitHubGHMatches, Level: level},
			Setup: onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, GitHub: onboard.SetupGitHub{User: user}}}
		for _, tc := range cases {
			writeGH(t, home, tc.gh)
			r := sp.Probe(context.Background(), []opssetup.ProbeRequest{req})[0]
			if r.Status == onboard.StatusWarn || r.Status == onboard.StatusFail {
				t.Errorf("%s (%s): %s, gh_matches is never a warning or failure", tc.name, level, r.Status)
			}
			if r.Status != tc.status || !strings.Contains(r.Detail, tc.has) || strings.HasSuffix(r.Detail, "is ") {
				t.Errorf("%s (%s): %+v", tc.name, level, r)
			}
		}
	}
}

// store.write's fix variant follows the push test's class: only a refused
// push says "You can read … but not write it"; a read that failed, a host
// that did not answer or another git failure never claims read works.
func TestStoreWriteFixVariant(t *testing.T) {
	t.Parallel()
	cases := []struct {
		class  probe.Class
		preset onboard.FixVariant
		want   onboard.FixVariant
	}{
		{probe.ClassNoWrite, "", onboard.FixDefault},
		{probe.ClassNoAccess, "", onboard.FixNoRead},
		{probe.ClassAuthDenied, "", onboard.FixNoRead},
		{probe.ClassHostKey, "", onboard.FixNoRead},
		{probe.ClassUnreachable, "", onboard.FixUnreachable},
		{probe.ClassFailed, "", onboard.FixWriteUnknown},
		{probe.ClassNoAccess, onboard.FixNotGitHub, onboard.FixNotGitHub},
	}
	for _, c := range cases {
		v := map[string]string{onboard.VarOwner: "octo-personal", onboard.VarRepo: "lore-me"}
		if c.preset != "" {
			v[onboard.VarFixVariant] = string(c.preset)
		}
		r := writeVars(outcome(probe.Outcome{Class: c.class, Detail: "fatal: Could not read from remote repository."}, v), c.class)
		got := onboard.FixVariant(r.Vars[onboard.VarFixVariant])
		if got != c.want {
			t.Errorf("%s: variant %q, want %q", c.class, got, c.want)
		}
		for _, step := range []onboard.StepID{onboard.StepPersonalStore, onboard.StepJoin} {
			text := onboard.FixFor(onboard.CheckStoreWrite, step, got).Text
			if c.class != probe.ClassNoWrite && strings.Contains(text, "can read") {
				t.Errorf("%s in %s: fix claims read works: %q", c.class, step, text)
			}
		}
	}
	// A write that passed gets no variant.
	if r := writeVars(outcome(probe.Outcome{Class: probe.ClassOK}, map[string]string{}), probe.ClassOK); r.Vars[onboard.VarFixVariant] != "" {
		t.Errorf("ok write got variant %q", r.Vars[onboard.VarFixVariant])
	}
}
