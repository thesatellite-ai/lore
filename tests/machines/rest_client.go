//go:build machines && !windows

package machines

// rest_client.go — the setup page's HTTP API on a Machine: StartPage runs
// the real `lore setup web --no-open --port 0` with the machine's HOME and
// environment, reads the link it prints, spends the one-time token the way
// web/src/api/client.ts does (POST /setup/session {token} with Origin →
// HttpOnly cookie + CSRF value + the route table) and then calls every
// operation through the route the session lists: GET with query
// parameters, anything else with a JSON body, X-Lore-CSRF and Origin.
//
//	Machine.StartPage(t) ─▶ lore setup web (cwd Home, PATH + lore's folder)
//	   └─ Page.Call(op, in, &out)  ─▶ /v1/… as the page sends it
//
// The server is killed (its process group) when the test ends.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// reSetupLink is the link `lore setup web` prints.
var reSetupLink = regexp.MustCompile(`http://127\.0\.0\.1:\d+/setup/#t=[0-9a-f]+`)

// pageStartTimeout bounds the wait for the printed link.
const pageStartTimeout = 60 * time.Second

// pageCallTimeout bounds one HTTP call (a step apply may probe ssh/gh).
const pageCallTimeout = 2 * time.Minute

// Page is one started setup page with a spent token (one page session).
type Page struct {
	origin string
	csrf   string
	routes map[string]pageRoute
	hc     *http.Client
	mu     sync.Mutex
	output strings.Builder
}

// pageRoute is one operation's route as the session lists it.
type pageRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	ID     string `json:"id,omitempty"`
}

// PageError is a non-2xx answer: the error envelope.
type PageError struct {
	Status int
	Body   errs.Body
}

func (e *PageError) Error() string {
	return fmt.Sprintf("HTTP %d %s: %s (hint: %s)", e.Status, e.Body.Code, e.Body.Message, e.Body.Hint)
}

// StartPage starts the page server on a free port in Home and opens a
// session; the server stops when t ends.
// pageLoreDir is the folder under the machine's Dir that puts the lore
// under test on the page server's PATH as `lore`.
const pageLoreDir = "page-bin"

func (m *Machine) StartPage(t testing.TB) *Page {
	t.Helper()
	c := exec.Command(m.bin, "setup", "web", "--no-open", "--port", "0")
	c.Dir = m.Home
	// lore on PATH, last, under that name: a fix the page runs may call
	// lore, and the binary under test need not be called lore (a task part
	// builds lore-machines-<part>), so a folder of the machine's own holds a
	// lore link to it.
	onPath := filepath.Join(m.Dir, pageLoreDir)
	if err := os.MkdirAll(onPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(m.bin, filepath.Join(onPath, "lore")); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	env := m.env()
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			env[i] = kv + ":" + onPath
		}
	}
	c.Env = env
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	pr, pw := io.Pipe()
	c.Stdout, c.Stderr = pw, pw
	if err := c.Start(); err != nil {
		t.Fatalf("start lore setup web: %v", err)
	}
	p := &Page{}
	t.Cleanup(func() {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		_ = c.Wait()
		_ = pw.Close()
	})
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		sent := false
		for sc.Scan() {
			p.mu.Lock()
			p.output.WriteString(sc.Text() + "\n")
			p.mu.Unlock()
			if u := reSetupLink.FindString(sc.Text()); u != "" && !sent {
				found <- u
				sent = true
			}
		}
	}()
	var link string
	select {
	case link = <-found:
	case <-time.After(pageStartTimeout):
		t.Fatalf("lore setup web printed no link in %s:\n%s", pageStartTimeout, p.Output())
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("setup link %q: %v", link, err)
	}
	p.origin = u.Scheme + "://" + u.Host
	_, token, _ := strings.Cut(link, "#t=")
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	p.hc = &http.Client{Jar: jar, Timeout: pageCallTimeout}
	var s struct {
		CSRF   string               `json:"csrf"`
		Routes map[string]pageRoute `json:"routes"`
	}
	if err := p.send(context.Background(), http.MethodPost, "/setup/session", map[string]string{"token": token}, &s); err != nil {
		t.Fatalf("setup session: %v\n%s", err, p.Output())
	}
	if s.CSRF == "" || len(s.Routes) == 0 {
		t.Fatalf("setup session: no csrf or routes")
	}
	p.csrf, p.routes = s.CSRF, s.Routes
	return p
}

// Output is what the server printed so far.
func (p *Page) Output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.output.String()
}

// Has reports whether the session lists op.
func (p *Page) Has(op string) bool { _, ok := p.routes[op]; return ok }

// Call runs one allowlisted operation: in is a JSON-tagged struct or map,
// out (a pointer, or nil) receives the answer. A non-2xx answer is a
// *PageError.
func (p *Page) Call(op string, in, out any) error {
	rt, ok := p.routes[op]
	if !ok {
		return fmt.Errorf("%s is not in the session's routes", op)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("%s: encode: %w", op, err)
	}
	fields := map[string]json.RawMessage{}
	if string(raw) != "null" {
		if err := json.Unmarshal(raw, &fields); err != nil {
			return fmt.Errorf("%s: input must be an object: %w", op, err)
		}
	}
	path := rt.Path
	if strings.Contains(path, "{id}") {
		f := rt.ID
		if f == "" {
			f = "id"
		}
		var id string
		_ = json.Unmarshal(fields[f], &id) // a missing id leaves {id} empty, as the page does
		path = strings.ReplaceAll(path, "{id}", url.PathEscape(id))
		delete(fields, f)
	}
	if out == nil {
		out = &json.RawMessage{}
	}
	ctx := context.Background()
	if rt.Method == http.MethodGet {
		q := url.Values{}
		for k, v := range fields {
			var s string
			if json.Unmarshal(v, &s) == nil {
				if s != "" {
					q.Add(k, s)
				}
				continue
			}
			if string(v) != "false" && string(v) != "null" {
				q.Add(k, string(v))
			}
		}
		if enc := q.Encode(); enc != "" {
			path += "?" + enc
		}
		return p.send(ctx, http.MethodGet, path, nil, out)
	}
	return p.send(ctx, rt.Method, path, fields, out)
}

// send sends one request; body nil = no body.
func (p *Page) send(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.origin+path, rd)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", p.origin)
		if p.csrf != "" {
			req.Header.Set("X-Lore-CSRF", p.csrf)
		}
	}
	res, err := p.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("%s %s: read: %w", method, path, err)
	}
	if res.StatusCode/100 != 2 {
		var env errs.Envelope
		if json.Unmarshal(raw, &env) != nil || env.Error.Code == "" {
			return fmt.Errorf("%s %s: HTTP %d: %s", method, path, res.StatusCode, raw)
		}
		return &PageError{Status: res.StatusCode, Body: env.Error}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: decode %T: %w: %s", method, path, out, err, raw)
	}
	return nil
}
