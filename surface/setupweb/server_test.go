package setupweb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/surface/rest"
)

// Fake operations: two on the allowlist (a GET list and a POST verb) and
// one that is not (task.purge), so the filter is visible.
type emptyIn struct{}

type listOut struct {
	Setups []string `json:"setups"`
}

type planIn struct {
	Kind string `flag:"kind" json:"kind,omitempty" desc:"setup kind"`
}

type planOut struct {
	Kind string `json:"kind"`
}

type purgeIn struct {
	ID string `arg:"0" json:"id" desc:"the row"`
}

type purgeOut struct {
	Purged int `json:"purged"`
}

// fakeRunner invokes the operation and counts the calls.
type fakeRunner struct {
	mu    sync.Mutex
	calls map[string]int
}

func (f *fakeRunner) Run(ctx context.Context, e op.Entry, d op.Decoder, _ op.Caller) (op.Result, error) {
	f.mu.Lock()
	f.calls[e.Spec().Name]++
	f.mu.Unlock()
	in, err := e.Decode(d)
	if err != nil {
		return op.Result{}, err
	}
	return e.Invoke(ctx, in)
}

func (f *fakeRunner) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

// showIn is setup.show's shape: the name is the first positional
// argument, so the route's {id} fills it.
type showIn struct {
	Name string `json:"name" arg:"0" desc:"the setup name"`
}

func fakeRegistry(t *testing.T) *op.Registry {
	t.Helper()
	errCodes := []errs.Code{errs.InvalidInput}
	reg, err := op.NewRegistry(
		op.Define(op.Spec{Name: OpSetupList, Summary: "List setups", Permission: op.PermRead, Access: op.AccessRead, Local: true, RESTNoun: "setup", Errors: errCodes},
			func(context.Context, emptyIn) (listOut, error) { return listOut{Setups: []string{"personal"}}, nil }),
		op.Define(op.Spec{Name: OpSetupPlan, Summary: "Plan a setup", Permission: op.PermRead, Access: op.AccessRead, Local: true, RESTNoun: "setup", Errors: errCodes},
			func(_ context.Context, in planIn) (planOut, error) { return planOut(in), nil }),
		// Its own noun keeps GET /v1/setup/plan a refused GET in
		// TestGETOnMutatingRoute (the real registry answers it with setup.show).
		op.Define(op.Spec{Name: OpSetupShow, Summary: "Show a setup", Permission: op.PermRead, Access: op.AccessRead, Local: true, RESTNoun: "setup_entry", Errors: errCodes},
			func(_ context.Context, in showIn) (showIn, error) { return in, nil }),
		op.Define(op.Spec{Name: "task.purge", Summary: "Purge a task", Permission: op.PermWrite, Access: op.AccessWrite, Errors: errCodes},
			func(context.Context, purgeIn) (purgeOut, error) { return purgeOut{Purged: 1}, nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// rig is a Page over a real listener (for its port), driven through its
// Handler with httptest so no socket traffic is needed.
type rig struct {
	t      *testing.T
	page   *Page
	runner *fakeRunner
	now    time.Time
	mu     sync.Mutex
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	r := &rig{t: t, runner: &fakeRunner{calls: map[string]int{}}, now: time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)}
	p, err := New(ln, Options{Registry: fakeRegistry(t), Runner: r.runner, LoreVersion: "test", Now: r.clock})
	if err != nil {
		t.Fatal(err)
	}
	r.page = p
	return r
}

func (r *rig) clock() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

func (r *rig) advance(d time.Duration) {
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
}

func (r *rig) origin() string { return "http://" + r.page.host }

// req builds a request with the page's own Host and, for a body, the
// page's Origin and JSON type; mod changes it before it is served.
func (r *rig) do(method, path, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	q := httptest.NewRequest(method, path, rd)
	q.Host = r.page.host
	if method != http.MethodGet {
		q.Header.Set("Content-Type", "application/json")
		q.Header.Set("Origin", r.origin())
	}
	if mod != nil {
		mod(q)
	}
	w := httptest.NewRecorder()
	r.page.Handler().ServeHTTP(w, q)
	return w
}

// session spends the token and returns the cookie and CSRF value.
func (r *rig) session() (*http.Cookie, string) {
	r.t.Helper()
	w := r.do(http.MethodPost, PathSession, `{"token":"`+r.page.token+`"}`, nil)
	if w.Code != http.StatusOK {
		r.t.Fatalf("session: %d %s", w.Code, w.Body)
	}
	var out sessionOut
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		r.t.Fatal(err)
	}
	cs := w.Result().Cookies()
	if len(cs) != 1 {
		r.t.Fatalf("want one cookie, got %d", len(cs))
	}
	return cs[0], out.CSRF
}

// authed adds the session cookie and CSRF header.
func authed(c *http.Cookie, csrf string) func(*http.Request) {
	return func(q *http.Request) {
		q.AddCookie(c)
		q.Header.Set(HeaderCSRF, csrf)
	}
}

func codeOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("not an error envelope: %s", w.Body)
	}
	return env.Error.Code
}

func TestTokenSingleUse(t *testing.T) {
	r := newRig(t)
	if w := r.do(http.MethodPost, PathSession, `{"token":"`+strings.Repeat("0", 64)+`"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", w.Code)
	}
	r.session()
	w := r.do(http.MethodPost, PathSession, `{"token":"`+r.page.token+`"}`, nil)
	if w.Code != http.StatusUnauthorized || codeOf(t, w) != string(errs.Unauthorized) || !strings.Contains(w.Body.String(), msgTokenUsed) {
		t.Fatalf("second use: %d %s", w.Code, w.Body)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("second use set a cookie")
	}
}

func TestCookieFlags(t *testing.T) {
	r := newRig(t)
	w := r.do(http.MethodPost, PathSession, `{"token":"`+r.page.token+`"}`, nil)
	set := w.Header().Get("Set-Cookie")
	for _, want := range []string{CookieName + "=", "Path=/", "HttpOnly", "SameSite=Strict"} {
		if !strings.Contains(set, want) {
			t.Errorf("Set-Cookie %q lacks %q", set, want)
		}
	}
	for _, bad := range []string{"Max-Age", "Expires", "Domain"} {
		if strings.Contains(set, bad) {
			t.Errorf("Set-Cookie %q has %q: the session must die with the server", set, bad)
		}
	}
	c := w.Result().Cookies()[0]
	if c.Value == r.page.token {
		t.Fatal("the cookie reuses the URL token")
	}
}

func TestCSRF(t *testing.T) {
	r := newRig(t)
	c, csrf := r.session()
	cases := map[string]func(*http.Request){
		"missing": func(q *http.Request) { q.AddCookie(c) },
		"wrong":   authed(c, strings.Repeat("a", 64)),
	}
	for name, mod := range cases {
		w := r.do(http.MethodPost, "/v1/setup/plan", `{"kind":"solo"}`, mod)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s csrf: %d %s", name, w.Code, w.Body)
		}
	}
	if n := r.runner.count(OpSetupPlan); n != 0 {
		t.Fatalf("op ran %d times without a valid CSRF value", n)
	}
	if w := r.do(http.MethodPost, "/v1/setup/plan", `{"kind":"solo"}`, authed(c, csrf)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"solo"`) {
		t.Fatalf("valid request: %d %s", w.Code, w.Body)
	}
	// Close is a mutation too.
	if w := r.do(http.MethodPost, PathClose, `{}`, func(q *http.Request) { q.AddCookie(c) }); w.Code != http.StatusUnauthorized {
		t.Fatalf("close without csrf: %d", w.Code)
	}
}

func TestNoCookie(t *testing.T) {
	r := newRig(t)
	_, csrf := r.session()
	w := r.do(http.MethodGet, "/v1/setup", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("GET without cookie: %d", w.Code)
	}
	w = r.do(http.MethodPost, "/v1/setup/plan", `{}`, func(q *http.Request) {
		q.Header.Set(HeaderCSRF, csrf)
		q.AddCookie(&http.Cookie{Name: CookieName, Value: strings.Repeat("b", 64)})
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("forged cookie: %d", w.Code)
	}
}

func TestContentTypeJSON(t *testing.T) {
	r := newRig(t)
	c, csrf := r.session()
	w := r.do(http.MethodPost, "/v1/setup/plan", `{}`, func(q *http.Request) {
		authed(c, csrf)(q)
		q.Header.Set("Content-Type", "text/plain")
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("text/plain: %d", w.Code)
	}
}

func TestOriginWrong(t *testing.T) {
	r := newRig(t)
	c, csrf := r.session()
	for _, o := range []string{"", "http://evil.example", "http://localhost:" + itoa(r.page.port), "null"} {
		w := r.do(http.MethodPost, "/v1/setup/plan", `{}`, func(q *http.Request) {
			authed(c, csrf)(q)
			if o == "" {
				q.Header.Del("Origin")
			} else {
				q.Header.Set("Origin", o)
			}
		})
		if w.Code != http.StatusUnauthorized {
			t.Errorf("origin %q: %d", o, w.Code)
		}
	}
	// The token exchange checks Origin too.
	r2 := newRig(t)
	w := r2.do(http.MethodPost, PathSession, `{"token":"`+r2.page.token+`"}`, func(q *http.Request) { q.Header.Set("Origin", "http://evil.example") })
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("session from another origin: %d", w.Code)
	}
}

func TestHostWrong(t *testing.T) {
	r := newRig(t)
	c, csrf := r.session()
	for _, h := range []string{"evil.example", "evil.example:" + itoa(r.page.port), "localhost:" + itoa(r.page.port), "127.0.0.1", "127.0.0.1:1"} {
		for _, path := range []string{PagePath, "/v1/setup", PathSession} {
			w := r.do(http.MethodGet, path, "", func(q *http.Request) {
				q.AddCookie(c)
				q.Header.Set(HeaderCSRF, csrf)
				q.Host = h
			})
			if w.Code != http.StatusBadRequest {
				t.Errorf("host %q %s: %d", h, path, w.Code)
			}
			if strings.Contains(w.Body.String(), csrf) {
				t.Errorf("host %q %s leaked the CSRF value", h, path)
			}
		}
	}
	if r.runner.count(OpSetupList) != 0 {
		t.Fatal("an op ran under a wrong Host")
	}
}

func TestGETOnMutatingRoute(t *testing.T) {
	r := newRig(t)
	c, csrf := r.session()
	for _, path := range []string{"/v1/setup/plan", "/v1/setup/plan?kind=solo", PathClose} {
		w := r.do(http.MethodGet, path, "", authed(c, csrf))
		if w.Code < 400 {
			t.Errorf("GET %s: %d", path, w.Code)
		}
	}
	if r.runner.count(OpSetupPlan) != 0 {
		t.Fatal("GET ran a mutating route")
	}
	select {
	case <-r.page.Done():
		t.Fatal("GET closed the page")
	default:
	}
}

func TestNotAllowlisted404(t *testing.T) {
	r := newRig(t)
	c, csrf := r.session()
	for _, q := range []struct{ method, path string }{
		{http.MethodPost, "/v1/tasks/x/purge"},
		{http.MethodGet, rest.PathSpec},
		{http.MethodGet, rest.PathOpenAPI},
		{http.MethodGet, "/v1/resources/host"},
		{http.MethodGet, "/setup/assets/index.html"},
		{http.MethodGet, "/setup/assets/server.go"},
	} {
		body := ""
		if q.method != http.MethodGet {
			body = `{}`
		}
		w := r.do(q.method, q.path, body, authed(c, csrf))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d %s", q.method, q.path, w.Code, w.Body)
		}
	}
	if r.runner.count("task.purge") != 0 {
		t.Fatal("a non-allowlisted op ran")
	}
	// A dot-dot path is cleaned by the mux (a redirect), never served.
	if w := r.do(http.MethodGet, "/setup/assets/../server.go", "", authed(c, csrf)); w.Code == http.StatusOK {
		t.Fatalf("dot-dot path served: %s", w.Body)
	}
	if w := r.do(http.MethodGet, "/v1/setup", "", authed(c, csrf)); w.Code != http.StatusOK {
		t.Fatalf("allowlisted GET: %d %s", w.Code, w.Body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	r := newRig(t)
	for _, path := range []string{PagePath, "/setup/team", "/setup/assets/opencore.css", "/setup/assets/missing.js", "/nope", "/v1/setup"} {
		w := r.do(http.MethodGet, path, "", nil)
		h := w.Header()
		if got := h.Get("Content-Security-Policy"); got != CSP {
			t.Errorf("%s CSP %q", path, got)
		}
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" || h.Get("Cache-Control") != "no-store" {
			t.Errorf("%s headers %v", path, h)
		}
	}
	// Even the DNS-rebinding refusal carries them.
	w := r.do(http.MethodGet, PagePath, "", func(q *http.Request) { q.Host = "evil.example" })
	if w.Code != http.StatusBadRequest || w.Header().Get("Content-Security-Policy") != CSP {
		t.Fatalf("rebinding: %d %v", w.Code, w.Header())
	}
	// ONB 5.3's policy tightened for the built UI (session.go CSP): no
	// 'unsafe-inline' anywhere, fonts from this server only, no form posts.
	const want = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	if CSP != want {
		t.Fatalf("CSP drifted: %q", CSP)
	}
}

// TestPageUsesNoInlineCode: the default page loads nothing from outside
// this server and carries no inline script or style (the CSP forbids
// both, so one would be a silently broken page).
func TestPageUsesNoInlineCode(t *testing.T) {
	idx, err := fs.ReadFile(DefaultUI().FS(), fileIndex)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<script", "<style", " style=", "http://", "https://"} {
		if strings.Contains(string(idx), bad) {
			t.Errorf("default index.html has %q", bad)
		}
	}
}

func TestIdleStop(t *testing.T) {
	r := newRig(t)
	if r.page.idle() {
		t.Fatal("idle at start")
	}
	r.advance(DefaultIdleMinutes*time.Minute - time.Second)
	if r.page.idle() {
		t.Fatal("idle too early")
	}
	c, csrf := r.session() // activity resets the clock
	r.advance(DefaultIdleMinutes*time.Minute - time.Second)
	r.do(http.MethodGet, "/v1/setup", "", authed(c, csrf))
	r.advance(time.Minute)
	if r.page.idle() {
		t.Fatal("a request did not count as activity")
	}
	r.advance(DefaultIdleMinutes * time.Minute)
	if !r.page.idle() {
		t.Fatal("not idle after the idle minutes")
	}
}

func TestServeEndToEnd(t *testing.T) {
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(ln, Options{Registry: fakeRegistry(t), Runner: &fakeRunner{calls: map[string]int{}}})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	var mu sync.Mutex
	w := writerFunc(func(b []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return out.Write(b) })
	opened := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- p.Serve(context.Background(), ln, w, func(_ context.Context, u string) error { opened <- u; return nil })
	}()
	u := <-opened
	if !strings.HasPrefix(u, "http://127.0.0.1:"+itoa(p.Port())+"/setup/#t=") || u != p.URL() {
		t.Fatalf("opened %q", u)
	}
	base := "http://127.0.0.1:" + itoa(p.Port())
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	cl := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	post := func(path, body, csrf string) *http.Response {
		q, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		q.Header.Set("Content-Type", "application/json")
		q.Header.Set("Origin", base)
		if csrf != "" {
			q.Header.Set(HeaderCSRF, csrf)
		}
		resp, err := cl.Do(q)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := post(PathSession, `{"token":"`+p.token+`"}`, "")
	var s sessionOut
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if rt, ok := s.Routes[OpSetupPlan]; !ok || rt.Method != http.MethodPost || rt.Path != "/v1/setup/plan" {
		t.Fatalf("routes %v", s.Routes)
	}
	// The page fills a path {id} from the field the route names (setup.show
	// takes the setup's name there); a route without {id} names none.
	if rt := s.Routes[OpSetupShow]; rt.Path != "/v1/setup_entry/{id}" || rt.ID != "name" {
		t.Fatalf("setup.show route %+v, want /v1/setup_entry/{id} filled from name", rt)
	}
	if rt := s.Routes[OpSetupPlan]; rt.ID != "" {
		t.Fatalf("setup.plan route %+v names an id field", rt)
	}
	if _, ok := s.Routes["task.purge"]; ok {
		t.Fatal("routes list a non-allowlisted op")
	}
	resp = post(PathClose, `{}`, s.CSRF)
	_ = resp.Body.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not stop on close")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(out.String(), "lore setup is open at "+p.URL()) || !strings.Contains(out.String(), "closes after 30 idle minutes") {
		t.Fatalf("announce: %q", out.String())
	}
}

func TestListenRefusesAPIPort(t *testing.T) {
	if _, err := Listen(rest.DefaultPort); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("port %d: %v", rest.DefaultPort, err)
	}
}

func TestOpenArgv(t *testing.T) {
	const u = "http://127.0.0.1:5/setup/#t=x"
	none := func(string) string { return "" }
	disp := func(k string) string {
		if k == "DISPLAY" {
			return ":0"
		}
		return ""
	}
	cases := []struct {
		goos   string
		env    func(string) string
		want   string
		wantOK bool
	}{
		{"darwin", none, "open " + u, true},
		{"windows", none, "rundll32 url.dll,FileProtocolHandler " + u, true},
		{"linux", disp, "xdg-open " + u, true},
		{"linux", none, "", false},
		{"freebsd", none, "", false},
	}
	for _, c := range cases {
		argv, ok := OpenArgv(c.goos, u, c.env)
		if ok != c.wantOK || strings.Join(argv, " ") != c.want {
			t.Errorf("%s: %v %v", c.goos, argv, ok)
		}
	}
}

func TestAllowlistShape(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range AllowedOps() {
		if seen[n] || !op.ValidName(n) {
			t.Errorf("bad or duplicate allowlist name %q", n)
		}
		seen[n] = true
	}
	for _, banned := range []string{"setup.run", "sql.run", "task.purge", "secrets.show"} {
		if Allowed(banned) {
			t.Errorf("%s must not be on the page allowlist", banned)
		}
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

func itoa(n int) string { return strconv.Itoa(n) }

// TestAllowlistMatchesSetupPageOps: the page's list is exactly
// ops/setup.PageOps (the onboarding ops, ONB 7.1) plus doctor.run, doctor.last, doctor.fix,
// whoami.show, identity.show, the two machine file ops (reveal, path
// list) and config.fmt (config_style's Run it). A new op on either side fails here, so
// the page never gains an operation without a reviewed edit of allow.go.
func TestAllowlistMatchesSetupPageOps(t *testing.T) {
	want := append(opssetup.PageOps(), OpDoctorRun, OpDoctorLast, OpDoctorFix, OpWhoamiShow, OpIdentityShow, OpMachineReveal, OpMachinePathList, OpConfigFmt)
	got := AllowedOps()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("allowlist %v\nwant      %v", got, want)
	}
}

// TestListenTakenPortIsPortInUse (batch not-built): a --port another
// program holds is E_PORT_IN_USE (it was E_UNSUPPORTED, "does not work
// here"), naming the port, the setup page and --port 0; lore api's own
// Listen gives the same code.
func TestListenTakenPortIsPortInUse(t *testing.T) {
	held, err := rest.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	port := held.Addr().(*net.TCPAddr).Port
	_, err = Listen(port)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.PortInUse || !strings.Contains(e.Message, "--port "+strconv.Itoa(port)) || !strings.Contains(e.Hint, "--port 0") {
		t.Fatalf("setup page on a taken port: %#v", err)
	}
	if _, err = rest.Listen(port); !errs.Has(err, errs.PortInUse) {
		t.Fatalf("lore api on a taken port: %v", err)
	}
}
