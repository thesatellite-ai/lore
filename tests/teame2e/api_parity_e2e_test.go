//go:build teame2e

package teame2e

// api_parity_e2e_test.go — spec 9.2: one Run path for every surface. The
// same input to `lore <noun> <verb>` and to `lore api`'s REST route gives
// the same refusal (same error code; CLI exit and HTTP status from the same
// class), and the same success once confirmed. Before the fix, `lore api`
// handed the engine itself to the REST server, so REST skipped the Mode B
// refusal, the --to <store>/<folder> confirm, the client-repo stores.yaml
// confirm (CFG7) and every other step of wire.App.Run.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// apiListenRe reads the address from `lore api`'s listening line.
var apiListenRe = regexp.MustCompile(`listening on (http://127\.0\.0\.1:[0-9]+)`)

// apiServer is a running `lore api --port 0` in one clone.
type apiServer struct {
	base, token string
	pid         int // the `lore api` process (its lock trace events)
}

// startAPI runs `lore api` in c (the actor's env), waits for its listening
// line and reads the token; the process is killed at cleanup.
func startAPI(t *testing.T, c *th.Clone) *apiServer {
	t.Helper()
	cmd := c.Command("api", "--port", "0")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = io.Discard
	if serr := cmd.Start(); serr != nil {
		t.Fatal(serr)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		var seen []string
		for sc.Scan() {
			seen = append(seen, sc.Text())
			if m := apiListenRe.FindStringSubmatch(sc.Text()); m != nil {
				found <- m[1]
				_, _ = io.Copy(io.Discard, stderr)
				return
			}
		}
		found <- "NOT STARTED: " + strings.Join(seen, " | ")
	}()
	var base string
	select {
	case base = <-found:
	case <-time.After(30 * time.Second):
		t.Fatal("lore api did not print its listening line")
	}
	if !strings.HasPrefix(base, "http://") {
		t.Fatalf("lore api: %s", base)
	}
	tok, err := os.ReadFile(c.A.LorePath("api-token"))
	if err != nil {
		t.Fatal(err)
	}
	return &apiServer{base: base, token: strings.TrimSpace(string(tok)), pid: cmd.Process.Pid}
}

// apiReply is one REST answer: status and the error code ("" on success).
type apiReply struct {
	status int
	code   string
	body   string
}

// post sends body to path with the bearer token.
func (s *apiServer) post(t *testing.T, path string, body map[string]any) apiReply {
	t.Helper()
	r, err := s.tryPost(path, body)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// tryPost is post for a goroutine other than the test's (t.Fatal there
// would end only that goroutine and leave the test waiting for its reply).
func (s *apiServer) tryPost(path string, body map[string]any) (apiReply, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return apiReply{}, err
	}
	req, err := http.NewRequest(http.MethodPost, s.base+path, bytes.NewReader(b))
	if err != nil {
		return apiReply{}, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return apiReply{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body) // a short read shows in the assertion
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env) // a success body has no error member
	return apiReply{status: resp.StatusCode, code: env.Error.Code, body: string(raw)}, nil
}

// cliCode is the [E_…] code a CLI refusal printed ("" when none).
func cliCode(r th.Result) string {
	m := regexp.MustCompile(`\[(E_[A-Z_]+)\]`).FindStringSubmatch(r.Out())
	if m == nil {
		return ""
	}
	return m[1]
}

// sameRefusal fails unless the CLI run and the REST reply refuse with code,
// at exit and status.
func sameRefusal(t *testing.T, what string, cli th.Result, rest apiReply, code string, exit, status int) {
	t.Helper()
	if cli.Code != exit || cliCode(cli) != code {
		t.Errorf("%s: CLI want %s exit %d, got exit %d: %s", what, code, exit, cli.Code, cli.Out())
	}
	if rest.status != status || rest.code != code {
		t.Errorf("%s: REST want %s %d, got %d %s: %s", what, code, status, rest.status, rest.code, rest.body)
	}
}

// `lore api` runs the whole time: the CLI commands run beside it (each REST
// request ends its live session like a CLI command, api_runner.go).
func TestAPIParity_SameRunPathAsCLI(t *testing.T) {
	a := newAgency(t)
	c := a.aliceApp
	a.storeMem(t, leakNote)
	dir := filepath.Join(a.alice.Home, "parity-store")
	pointer := filepath.Join(c.Dir, ".lore", "lore.yaml")
	if _, err := os.Stat(pointer); err == nil {
		t.Fatalf("precondition: %s must not exist yet", pointer)
	}
	modeB := func(on bool) {
		t.Helper()
		if !on {
			if err := os.Remove(pointer); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := os.WriteFile(pointer, []byte("mode: shared\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	srv := startAPI(t, c)

	// The CLI.
	newFolder := c.Lore("memory", "add", "--body", "cli note into a new folder", "--to", "northwind/parity-cli")
	c.Lore("memory", "add", "--body", "cli note into a new folder", "--to", "northwind/parity-cli", "--yes").OK(t)
	leak := c.Lore("memory", "add", "--body", leakNote, "--to", "repo")
	cfg7 := c.Lore("store", "init", "parity", fileuri.URL(dir, ""))
	modeB(true)
	retired := c.Lore("memory", "add", "--body", "after mode b")
	modeB(false)

	// The same inputs over REST.
	rNewFolder := srv.post(t, "/v1/memories", map[string]any{"body": "rest note into a new folder", "to": "northwind/parity-rest"})
	rYes := srv.post(t, "/v1/memories", map[string]any{"body": "rest note into a new folder", "to": "northwind/parity-rest", "yes": true})
	rLeak := srv.post(t, "/v1/memories", map[string]any{"body": leakNote, "to": "repo"})
	rCFG7 := srv.post(t, "/v1/stores/init", map[string]any{"name": "parity", "url": fileuri.URL(dir, "")})
	modeB(true)
	rRetired := srv.post(t, "/v1/memories", map[string]any{"body": "after mode b"})
	modeB(false)

	// (3) --to <store>/<new folder>: both ask first (E_NEEDS_CONFIRM, CLI
	// 26 / HTTP 428), then both write once confirmed.
	sameRefusal(t, "new store folder", newFolder, rNewFolder, "E_NEEDS_CONFIRM", 26, http.StatusPreconditionRequired)
	if rYes.status/100 != 2 {
		t.Errorf("REST write with yes: %d %s", rYes.status, rYes.body)
	}
	// (2a) client repo: internal text written to the repo is refused by the
	// leak guard on both.
	sameRefusal(t, "leak into the client repo", leak, rLeak, "E_LEAK_BLOCKED", 19, http.StatusForbidden)
	// (2b) client repo: attaching a store edits the client's stores.yaml,
	// which asks first (CFG7) on both.
	sameRefusal(t, "client-repo stores.yaml (CFG7)", cfg7, rCFG7, "E_NEEDS_CONFIRM", 26, http.StatusPreconditionRequired)
	// (1) a Mode B repo: every operation gives the retired-mode refusal.
	sameRefusal(t, "Mode B", retired, rRetired, "E_UNSUPPORTED", 23, http.StatusConflict)
	if !strings.Contains(retired.Out(), "retired shared-database mode") || !strings.Contains(rRetired.body, "retired shared-database mode") {
		t.Errorf("Mode B: both name the retired mode: CLI %s / REST %s", retired.Out(), rRetired.body)
	}
}

// A running `lore api` does not hold the repo's live lock between
// requests: a CLI write beside it gets the lock without waiting, with live
// rows on, and CLI and REST writers at the same time lose nothing.
//
// Both facts are read from the lock trace (adapters/locktrace), never from
// a clock: once a REST answer is back, the API process holds no live lock,
// and the CLI write after it never finds the live lock busy. A machine
// under load makes the write slower, not either fact false.
func TestAPIParity_CLIBesideRunningAPI(t *testing.T) {
	a := newAgency(t)
	c := a.aliceApp
	// the trace names locks by their resolved path (/private/var/... for a
	// macOS /var/... temp folder)
	dir, err := filepath.EvalSymlinks(c.Dir)
	if err != nil {
		t.Fatal(err)
	}
	liveLock := filepath.Join(dir, ".git", "lore", "live.lock")
	srv := startAPI(t, c)
	if r := srv.post(t, "/v1/tasks", map[string]any{"title": "rest task before", "to": "repo", "commitment": "accepted"}); r.status/100 != 2 {
		t.Fatalf("REST task add: %d %s", r.status, r.body)
	}
	before := a.w.LockTrace()
	if len(before.Of(liveLock)) == 0 {
		t.Fatalf("the REST task add traced no live lock (live rows off?): %v", before)
	}
	if slices.Contains(before.Held()[liveLock], srv.pid) {
		t.Fatalf("lore api (pid %d) still holds %s after its answer: a CLI beside it waits for the lock (events %v)", srv.pid, liveLock, before.Of(liveLock))
	}
	r := c.MustLore("task", "add", "cli task beside the api", "--to", "repo", "--commitment", "accepted")
	r.Never(t, "live lock", "processlock")
	cli := a.w.LockTrace()[len(before):].Of(liveLock)
	taken := false
	for _, e := range cli {
		switch e.Kind {
		case th.LockBusy:
			t.Errorf("the CLI write beside an idle lore api found the live lock busy: %s (events %v)", e, cli)
		case th.LockTaken:
			taken = true
		}
	}
	if !taken {
		t.Fatalf("the CLI write traced no live lock take (events %v): the check would pass vacuously", cli)
	}

	// Concurrent writers: three over REST, three over the CLI.
	suffixes := []string{"a", "b", "c"}
	var ps []*th.Pending
	for _, s := range suffixes {
		ps = append(ps, c.Prepare("task", "add", "cli parallel "+s, "--to", "repo", "--commitment", "accepted"))
	}
	done := make(chan apiReply, len(suffixes))
	for _, s := range suffixes {
		go func(s string) {
			r, err := srv.tryPost("/v1/tasks", map[string]any{"title": "rest parallel " + s, "to": "repo", "commitment": "accepted"})
			if err != nil {
				r = apiReply{body: err.Error()} // status 0: reported below
			}
			done <- r
		}(s)
	}
	for _, res := range a.w.Simultaneously(ps...) {
		res.OK(t)
	}
	for range 3 {
		if rr := <-done; rr.status/100 != 2 {
			t.Errorf("REST parallel task add: %d %s", rr.status, rr.body)
		}
	}
	out := c.MustLore("task", "list", "--all").Out()
	for _, want := range []string{"rest task before", "cli task beside the api", "cli parallel a", "cli parallel b", "cli parallel c", "rest parallel a", "rest parallel b", "rest parallel c"} {
		if !strings.Contains(out, want) {
			t.Errorf("lost %q: %s", want, out)
		}
	}
}
