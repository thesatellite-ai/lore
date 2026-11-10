//go:build userscen

package userscen

// rest_harness_test.go — shared helpers of the `rest` area (the local
// `lore api` REST surface): a repo the person initialised, a running
// `lore api --port 0` started from the binary under test, and a small HTTP
// client that returns status, headers, body and the decoded error
// envelope. Every helper name starts with rest so it never collides with
// another area's helpers in this package.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// restListenRe reads the address from `lore api`'s listening line. It only
// matches a loopback address: a line naming any other host fails the start.
var restListenRe = regexp.MustCompile(`lore api listening on (http://127\.0\.0\.1:([0-9]+)) \(token: ([^)]+)\)`)

// restAnyListenRe matches the listening line whatever host it names, so a
// non-loopback bind is reported instead of looking like "never started".
var restAnyListenRe = regexp.MustCompile(`lore api listening on http://\S+`)

// restCLICodeRe finds the [E_…] code a human-mode CLI refusal printed.
var restCLICodeRe = regexp.MustCompile(`\[(E_[A-Z0-9_]+)\]`)

// restStartTimeout bounds how long `lore api` may take to print its line.
const restStartTimeout = 30 * time.Second

// restWorld is one person with one initialised repo.
type restWorld struct {
	w     *th.World
	alice *th.Actor
	repo  *th.Clone
}

// newRestWorld builds alice's own repo (octo-personal/app), `lore init`ed.
func newRestWorld(t *testing.T) *restWorld {
	t.Helper()
	w := newWorld(t)
	alice := w.Human("alice", "person@example.com")
	repo := alice.Clone(w.Remote("github.com/octo-personal/app"), "app")
	repo.MustLore("init", "--non-interactive", "--name=app")
	return &restWorld{w: w, alice: alice, repo: repo}
}

// restAPI is a running `lore api`.
type restAPI struct {
	base      string // http://127.0.0.1:<port>
	port      string
	tokenPath string // the path the listening line named
	token     string
	cmd       *exec.Cmd
}

// restStart runs `lore api --port 0` (plus extra flags) in c and waits for
// its listening line; the process is stopped at cleanup.
func restStart(t *testing.T, c *th.Clone, extra ...string) *restAPI {
	t.Helper()
	cmd := c.Command(append([]string{"api", "--port", "0"}, extra...)...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	found := make(chan []string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		var seen []string
		for sc.Scan() {
			line := sc.Text()
			seen = append(seen, line)
			if m := restListenRe.FindStringSubmatch(line); m != nil {
				found <- m
				_, _ = io.Copy(io.Discard, stderr)
				return
			}
			if restAnyListenRe.MatchString(line) {
				found <- []string{"NOT LOOPBACK: " + line}
				return
			}
		}
		found <- []string{"NOT STARTED: " + strings.Join(seen, " | ")}
	}()
	var m []string
	select {
	case m = <-found:
	case <-time.After(restStartTimeout):
		t.Fatal("lore api did not print its listening line")
	}
	if len(m) != 4 {
		t.Fatalf("lore api: %s", m[0])
	}
	s := &restAPI{base: m[1], port: m[2], tokenPath: m[3], cmd: cmd}
	s.token = restReadToken(t, c.A)
	return s
}

// restReadToken reads ~/.lore/api-token of a.
func restReadToken(t *testing.T, a *th.Actor) string {
	t.Helper()
	b, err := os.ReadFile(a.LorePath("api-token"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// restErr is the spec 9.4 error object, as both the CLI (--json) and the
// REST surface print it.
type restErr struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Hint        string `json:"hint"`
	Exit        int    `json:"exit"`
	Class       string `json:"class"`
	Retryable   bool   `json:"retryable"`
	NeedsPerson bool   `json:"needs_person"`
}

// restEnvelope wraps restErr.
type restEnvelope struct {
	Error *restErr `json:"error"`
}

// restReply is one HTTP answer.
type restReply struct {
	status int
	header http.Header
	body   string
	err    *restErr // nil on a body without an error member
}

// restReq is one request; header values are set (not added) in order.
type restReq struct {
	method, path, body string
	token              *string // nil: the server's token; "" pointer: none
	header             []string
}

// restNoToken is a request without any Authorization header.
var restNoToken = func() *string { s := ""; return &s }()

// do sends r and returns the reply; a transport failure fails the test.
func (s *restAPI) do(t *testing.T, r restReq) restReply {
	t.Helper()
	rep, err := s.try(r)
	if err != nil {
		t.Fatalf("%s %s: %v", r.method, r.path, err)
	}
	return rep
}

// try is do without failing the test (concurrent callers).
func (s *restAPI) try(r restReq) (restReply, error) {
	var body io.Reader
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	req, err := http.NewRequest(r.method, s.base+r.path, body)
	if err != nil {
		return restReply{}, err
	}
	tok := s.token
	if r.token != nil {
		tok = *r.token
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if r.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(r.header); i += 2 {
		req.Header.Set(r.header[i], r.header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return restReply{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return restReply{}, err
	}
	rep := restReply{status: resp.StatusCode, header: resp.Header, body: string(raw)}
	var env restEnvelope
	if json.Unmarshal(raw, &env) == nil {
		rep.err = env.Error
	}
	return rep, nil
}

// get, post are shorthands with the server's token.
func (s *restAPI) get(t *testing.T, path string) restReply {
	t.Helper()
	return s.do(t, restReq{method: http.MethodGet, path: path})
}

func (s *restAPI) post(t *testing.T, path, body string) restReply {
	t.Helper()
	return s.do(t, restReq{method: http.MethodPost, path: path, body: body})
}

// restCLIErr decodes the error envelope a `--json` CLI run printed (stdout
// or stderr); nil when the run printed none.
func restCLIErr(r th.Result) *restErr {
	for _, out := range []string{r.Stdout, r.Stderr} {
		var env restEnvelope
		if json.Unmarshal(bytes.TrimSpace([]byte(out)), &env) == nil && env.Error != nil {
			return env.Error
		}
	}
	return nil
}

// restJSONEqual reports whether two JSON documents hold the same value
// (key order and whitespace ignored).
func restJSONEqual(a, b []byte) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	var va, vb json.RawMessage
	if json.Unmarshal(ca.Bytes(), &va) != nil || json.Unmarshal(cb.Bytes(), &vb) != nil {
		return false
	}
	na, errA := restCanon(va)
	nb, errB := restCanon(vb)
	return errA == nil && errB == nil && na == nb
}

// restCanon re-encodes a JSON value with object keys sorted (encoding/json
// sorts map keys), so two equal documents give the same string.
func restCanon(v json.RawMessage) (string, error) {
	t := bytes.TrimSpace(v)
	switch {
	case len(t) > 0 && t[0] == '{':
		var m map[string]json.RawMessage
		if err := json.Unmarshal(t, &m); err != nil {
			return "", err
		}
		out := map[string]json.RawMessage{}
		for k, x := range m {
			c, err := restCanon(x)
			if err != nil {
				return "", err
			}
			out[k] = json.RawMessage(c)
		}
		b, err := json.Marshal(out)
		return string(b), err
	case len(t) > 0 && t[0] == '[':
		var l []json.RawMessage
		if err := json.Unmarshal(t, &l); err != nil {
			return "", err
		}
		out := make([]json.RawMessage, len(l))
		for i, x := range l {
			c, err := restCanon(x)
			if err != nil {
				return "", err
			}
			out[i] = json.RawMessage(c)
		}
		b, err := json.Marshal(out)
		return string(b), err
	}
	var c bytes.Buffer
	if err := json.Compact(&c, t); err != nil {
		return "", err
	}
	return c.String(), nil
}
