//go:build userscen

package userscen

// rest_auth_test.go — who may call `lore api` (STORE_SERVER_SPEC 9.7,
// SERVER SCOPE: closed by default even on 127.0.0.1), driven through the
// real binary:
//
//   - every route, the two documents and an unknown path refuse a caller
//     without the right bearer token: none, wrong, another person's, Basic,
//     lower-case scheme, padded, prefix/suffix of the right one, and the
//     refusal is 401 E_UNAUTHORIZED with WWW-Authenticate and the hint;
//   - a refused write never lands;
//   - a cross-origin (browser) caller gets no CORS header, with or without
//     the token, and a preflight is refused;
//   - the token file: created 0600 hex on first start, the same token
//     after a restart, `lore api token rotate` takes effect on a RUNNING
//     server (old token 401, new token 200), and a token file someone else
//     could read, garbage, or a symlink stops `lore api` from starting;
//   - binding: the server listens on 127.0.0.1 only (a non-loopback address
//     of the machine refuses the connection), --port out of range or not a
//     number is refused, a port already in use is reported.

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// restUnauthorizedExit is E_UNAUTHORIZED's exit code (docs/reference/errors.md).
const restUnauthorizedExit = 28

// restHexToken is what a lore api token looks like: 32 random bytes as hex.
var restHexToken = regexp.MustCompile(`^[0-9a-f]{64}$`)

// restRefused fails unless rep is the closed-by-default refusal.
func restRefused(t *testing.T, what string, rep restReply) {
	t.Helper()
	restRefusedHint(t, what, rep, "api-token")
}

// restRefusedHint is restRefused with the next step the hint must name:
// a wrong bearer points at the token file, a server whose token file is
// gone or unusable points at lore api token rotate (the path stays on the
// server, the caller is not yet trusted).
func restRefusedHint(t *testing.T, what string, rep restReply, hint string) {
	t.Helper()
	if rep.status != http.StatusUnauthorized || rep.err == nil || rep.err.Code != "E_UNAUTHORIZED" {
		t.Errorf("%s: want 401 E_UNAUTHORIZED, got %d %.300s", what, rep.status, rep.body)
		return
	}
	if rep.err.Exit != restUnauthorizedExit || rep.err.Class != "unauthorized" || !strings.Contains(rep.err.Hint, hint) {
		t.Errorf("%s: refusal envelope %+v", what, *rep.err)
	}
	if rep.header.Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("%s: WWW-Authenticate %q", what, rep.header.Get("WWW-Authenticate"))
	}
	restNoCORS(t, what, rep)
}

// restNoCORS fails when a reply carries any Access-Control-* header: a
// browser page on another origin must never be allowed to read lore.
func restNoCORS(t *testing.T, what string, rep restReply) {
	t.Helper()
	for k := range rep.header {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("%s: CORS header %s: %v", what, k, rep.header[k])
		}
	}
}

// TestRestAuth_ClosedByDefault: every kind of wrong caller is refused on
// every kind of route, and nothing a refused caller sent is written.
func TestRestAuth_ClosedByDefault(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	if !restHexToken.MatchString(srv.token) {
		t.Fatalf("token file holds %d chars, not a 64-hex token", len(srv.token))
	}
	if want := rw.alice.LorePath("api-token"); srv.tokenPath != want {
		t.Errorf("listening line names token %s, want %s", srv.tokenPath, want)
	}
	// bob's own token (another person on the same machine) is not alice's.
	bob := rw.w.Human("bob", "bob@example.com")
	bob.Lore("scratch", "api", "token", "rotate").OK(t)
	bobTok := restReadToken(t, bob)

	routes := []restReq{
		{method: http.MethodGet, path: "/openapi.json"},
		{method: http.MethodGet, path: "/v1/spec"},
		{method: http.MethodGet, path: "/v1/memories"},
		{method: http.MethodPost, path: "/v1/memories", body: `{"body":"written by a stranger"}`},
		{method: http.MethodPost, path: "/v1/tasks", body: `{"title":"stranger task"}`},
		{method: http.MethodDelete, path: "/v1/memories/mem_01a1b49cf238767ca425d36a07b5cc1e"},
		{method: http.MethodGet, path: "/v1/no-such-route"},
		{method: http.MethodGet, path: "/"},
	}
	str := func(s string) *string { return &s }
	tokens := map[string]*string{
		"none":                  restNoToken,
		"wrong":                 str(strings.Repeat("f", 64)),
		"bob's token":           str(bobTok),
		"prefix of the right":   str(srv.token[:32]),
		"right plus a char":     str(srv.token + "0"),
		"upper-cased right one": str(strings.ToUpper(srv.token)),
	}
	for _, rt := range routes {
		for name, tok := range tokens {
			r := rt
			r.token = tok
			restRefused(t, rt.method+" "+rt.path+" with "+name, srv.do(t, r))
		}
		// The right token under another scheme or a lower-case scheme. (Trailing
		// whitespace is not a case: HTTP strips it from every header value.)
		for _, auth := range []string{"Basic " + srv.token, "bearer " + srv.token, "Token " + srv.token, srv.token, "Bearer"} {
			r := rt
			r.token = restNoToken
			r.header = []string{"Authorization", auth}
			restRefused(t, rt.method+" "+rt.path+" with Authorization "+strings.Fields(auth)[0], srv.do(t, r))
		}
		// The token in a query parameter or a cookie is not a credential.
		r := rt
		r.token = restNoToken
		r.path += "?token=" + srv.token
		restRefused(t, rt.method+" "+rt.path+" token in query", srv.do(t, r))
		r = rt
		r.token = restNoToken
		r.header = []string{"Cookie", "token=" + srv.token}
		restRefused(t, rt.method+" "+rt.path+" token in cookie", srv.do(t, r))
	}
	// Nothing the refused callers sent was written.
	list := rw.repo.MustLore("memory", "list", "--json").Out()
	if strings.Contains(list, "written by a stranger") {
		t.Errorf("a refused POST wrote a memory: %s", list)
	}
	if out := rw.repo.MustLore("task", "list", "--all", "--json").Out(); strings.Contains(out, "stranger task") {
		t.Errorf("a refused POST wrote a task: %s", out)
	}
	// With the right token the same routes answer (no route is 404 by
	// mistake, the unknown one is E_NOT_FOUND naming the route).
	if r := srv.get(t, "/v1/memories"); r.status != http.StatusOK {
		t.Errorf("right token: %d %s", r.status, r.body)
	}
	if r := srv.get(t, "/v1/no-such-route"); r.status != http.StatusNotFound || r.err == nil || r.err.Code != "E_NOT_FOUND" || !strings.Contains(r.err.Message, "no route GET /v1/no-such-route") {
		t.Errorf("unknown route with the right token: %d %s", r.status, r.body)
	}
}

// TestRestAuth_CrossOrigin: a page on another origin cannot use lore api.
// No reply carries a CORS header, so a browser never hands the page the
// response; a preflight (OPTIONS, no token, as browsers send it) is
// refused; every reply is JSON with nosniff.
func TestRestAuth_CrossOrigin(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	origin := []string{"Origin", "https://evil.example"}
	cases := []restReq{
		{method: http.MethodGet, path: "/v1/memories", header: origin},
		{method: http.MethodGet, path: "/v1/memories", header: origin, token: restNoToken},
		{method: http.MethodPost, path: "/v1/memories", body: `{"body":"from a page"}`, header: origin},
		{method: http.MethodGet, path: "/openapi.json", header: origin},
		{method: http.MethodOptions, path: "/v1/memories", token: restNoToken, header: append(append([]string{}, origin...),
			"Access-Control-Request-Method", "POST", "Access-Control-Request-Headers", "authorization,content-type")},
		{method: http.MethodOptions, path: "/v1/memories", header: origin},
	}
	for _, c := range cases {
		rep := srv.do(t, c)
		what := c.method + " " + c.path
		restNoCORS(t, what, rep)
		if ct := rep.header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type %q", what, ct)
		}
		if rep.header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: no nosniff", what)
		}
		if c.method == http.MethodOptions && rep.status/100 == 2 {
			t.Errorf("%s: a preflight answered %d", what, rep.status)
		}
		if c.token == restNoToken {
			restRefused(t, what+" without token", rep)
		}
	}
	// An OPTIONS with the token is no route (405/404 class), never an allow.
	if rep := srv.do(t, restReq{method: http.MethodOptions, path: "/v1/memories"}); rep.status/100 == 2 || rep.header.Get("Allow") != "" && rep.status/100 == 2 {
		t.Errorf("OPTIONS with token: %d %v", rep.status, rep.header)
	}
}

// hasModeBits: the system keeps POSIX file modes (Windows reports 0666 for
// every writable file, so no mode is checked there).
var hasModeBits = runtime.GOOS != "windows"

// TestRestAuth_TokenFileLifecycle: the token is made once (0600, hex), a
// restarted server keeps it, a rotate takes effect on the running server,
// and the rotate output names the file.
func TestRestAuth_TokenFileLifecycle(t *testing.T) {
	rw := newRestWorld(t)
	tokPath := rw.alice.LorePath("api-token")
	if _, err := os.Stat(tokPath); err == nil {
		t.Fatalf("precondition: %s exists before the first lore api", tokPath)
	}
	srv := restStart(t, rw.repo)
	st, err := os.Stat(tokPath)
	if err != nil {
		t.Fatal(err)
	}
	if hasModeBits && st.Mode().Perm() != 0o600 {
		t.Errorf("token file mode %o, want 600", st.Mode().Perm())
	}
	first := srv.token
	if r := srv.get(t, "/v1/memories"); r.status != http.StatusOK {
		t.Fatalf("first token: %d %s", r.status, r.body)
	}
	// A restart reuses the token: clients configured with it keep working.
	_ = srv.cmd.Process.Kill()
	_ = srv.cmd.Wait()
	srv2 := restStart(t, rw.repo)
	if srv2.token != first {
		t.Errorf("restart changed the token")
	}
	if r := srv2.get(t, "/v1/memories"); r.status != http.StatusOK {
		t.Fatalf("token after restart: %d %s", r.status, r.body)
	}

	// Rotate while the server runs: the help promises "every client holding
	// the old one gets 401", and the new token is the one that works.
	rot := rw.repo.MustLore("api", "token", "rotate")
	rot.Says(t, "new token written to "+tokPath)
	if strings.Contains(rot.Out(), first) {
		t.Errorf("rotate printed a token")
	}
	second := restReadToken(t, rw.alice)
	if second == first || !restHexToken.MatchString(second) {
		t.Fatalf("rotate did not write a new hex token")
	}
	if st, err := os.Stat(tokPath); err != nil || (hasModeBits && st.Mode().Perm() != 0o600) {
		t.Errorf("rotated token file mode: %v %v", st, err)
	}
	old := first
	restRefused(t, "old token after rotate on the running server", srv2.do(t, restReq{method: http.MethodGet, path: "/v1/memories", token: &old}))
	if r := srv2.do(t, restReq{method: http.MethodGet, path: "/v1/memories", token: &second}); r.status != http.StatusOK {
		t.Errorf("new token after rotate on the running server: %d %s", r.status, r.body)
	}
	// Rotating twice in a row: only the last token works.
	rw.repo.MustLore("api", "token", "rotate")
	third := restReadToken(t, rw.alice)
	restRefused(t, "second-to-last token", srv2.do(t, restReq{method: http.MethodGet, path: "/v1/spec", token: &second}))
	if r := srv2.do(t, restReq{method: http.MethodGet, path: "/v1/spec", token: &third}); r.status != http.StatusOK {
		t.Errorf("last token: %d", r.status)
	}
	// The token file removed while the server runs: nobody gets in (closed),
	// and a rotate brings a working token back.
	if err := os.Remove(tokPath); err != nil {
		t.Fatal(err)
	}
	gone := srv2.do(t, restReq{method: http.MethodGet, path: "/v1/memories", token: &third})
	restRefusedHint(t, "token file removed", gone, "lore api token rotate")
	if gone.err != nil && gone.err.Message != "the server's api token file is missing or unusable" {
		t.Errorf("token file removed: message %q", gone.err.Message)
	}
	rw.repo.MustLore("api", "token", "rotate")
	fourth := restReadToken(t, rw.alice)
	if r := srv2.do(t, restReq{method: http.MethodGet, path: "/v1/memories", token: &fourth}); r.status != http.StatusOK {
		t.Errorf("token after re-creating the file: %d %s", r.status, r.body)
	}
}

// TestRestAuth_UnsafeTokenFileRefused: lore api never trusts a token file
// another user could read, one that is not a token, or a symlink; each
// refusal names the file and the fix, and the fix works.
func TestRestAuth_UnsafeTokenFileRefused(t *testing.T) {
	rw := newRestWorld(t)
	tokPath := rw.alice.LorePath("api-token")
	good := strings.Repeat("ab", 32)
	writeTok := func(body string, mode os.FileMode) {
		t.Helper()
		_ = os.Remove(tokPath)
		if err := os.MkdirAll(filepath.Dir(tokPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tokPath, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(tokPath, mode); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		setup func()
		say   []string
	}{
		{"group readable", func() { writeTok(good+"\n", 0o640) }, []string{"E_CONFIG_INVALID", tokPath, "has mode 640", "other users could read it", "chmod 600 " + tokPath}},
		{"world readable", func() { writeTok(good+"\n", 0o644) }, []string{"E_CONFIG_INVALID", "has mode 644", "chmod 600"}},
		{"garbage", func() { writeTok("not a token\n", 0o600) }, []string{"E_CONFIG_INVALID", tokPath + " does not hold a lore api token", "lore api token rotate"}},
		{"short hex", func() { writeTok("abcd\n", 0o600) }, []string{"E_CONFIG_INVALID", "does not hold a lore api token"}},
		{"empty", func() { writeTok("", 0o600) }, []string{"E_CONFIG_INVALID", "does not hold a lore api token"}},
		{"symlink", func() {
			target := filepath.Join(rw.alice.Home, "elsewhere-token")
			if err := os.WriteFile(target, []byte(good+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_ = os.Remove(tokPath)
			if err := os.Symlink(target, tokPath); err != nil {
				t.Fatal(err)
			}
		}, []string{"E_CONFIG_INVALID", tokPath + " is not a regular file", "remove it"}},
		{"directory", func() {
			_ = os.Remove(tokPath)
			if err := os.MkdirAll(tokPath, 0o700); err != nil {
				t.Fatal(err)
			}
		}, []string{"E_CONFIG_INVALID", "is not a regular file"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !hasModeBits && strings.HasSuffix(c.name, " readable") {
				t.Skip("Windows has no mode bits: lore checks no token file mode there")
			}
			c.setup()
			r := rw.repo.Lore("api", "--port", "0")
			r.Fails(t, 20, c.say...)
			r.Never(t, "listening on")
			_ = os.RemoveAll(tokPath)
		})
	}
	// The documented fix for the mode case works: chmod 600, then it starts
	// with that very token.
	writeTok(good+"\n", 0o644)
	if err := os.Chmod(tokPath, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := restStart(t, rw.repo)
	if srv.token != good {
		t.Errorf("after chmod 600 the server must use the existing token")
	}
	if r := srv.get(t, "/v1/memories"); r.status != http.StatusOK {
		t.Errorf("after chmod: %d %s", r.status, r.body)
	}
}

// TestRestAuth_LoopbackOnly: lore api binds 127.0.0.1 and nothing else.
// Every non-loopback address of the machine refuses a connection on the
// served port; the listening line names 127.0.0.1.
func TestRestAuth_LoopbackOnly(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo) // restStart fails on any non-127.0.0.1 line
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	tried := 0
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() {
			continue
		}
		tried++
		conn, derr := net.DialTimeout("tcp", net.JoinHostPort(ipn.IP.String(), srv.port), 2*time.Second)
		if derr == nil {
			_ = conn.Close()
			t.Errorf("lore api answered on non-loopback %s:%s", ipn.IP, srv.port)
		}
	}
	// IPv6 loopback is not served either (only 127.0.0.1 is promised).
	if conn, derr := net.DialTimeout("tcp", net.JoinHostPort("::1", srv.port), 2*time.Second); derr == nil {
		_ = conn.Close()
		t.Errorf("lore api answered on [::1]:%s", srv.port)
	}
	t.Logf("checked %d non-loopback addresses", tried)
	// The documented default port is 7461 (the --port help says so).
	help := rw.repo.MustLore("api", "--help").Out()
	if !strings.Contains(help, "7461") || !strings.Contains(help, "127.0.0.1") {
		t.Errorf("lore api --help must name 127.0.0.1 and the default port 7461: %s", help)
	}
}

// TestRestAuth_PortFlag: --port out of range, not a number, or already in
// use is refused with a message naming the port; nothing starts.
func TestRestAuth_PortFlag(t *testing.T) {
	rw := newRestWorld(t)
	for _, p := range []string{"-1", "65536", "99999"} {
		r := rw.repo.Lore("api", "--port", p)
		r.Fails(t, 20, "E_INVALID_INPUT", "--port must be 0 to 65535")
		r.Never(t, "listening on")
	}
	for _, p := range []string{"abc", "1.5", ""} {
		r := rw.repo.Lore("api", "--port", p)
		if r.Code == 0 || strings.Contains(r.Out(), "listening on") {
			t.Errorf("--port %q started: %s", p, r.Out())
		}
		if !strings.Contains(r.Out(), "--port") {
			t.Errorf("--port %q: the refusal must name the flag: %s", p, r.Out())
		}
	}
	// A port someone else holds: the error names the port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	busy := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	r := rw.repo.Lore("api", "--port", busy)
	if r.Code == 0 || !strings.Contains(r.Out(), "port "+busy) || strings.Contains(r.Out(), "listening on") {
		t.Errorf("busy port %s: exit %d %s", busy, r.Code, r.Out())
	}
	// Two lore api at once on --port 0 each get their own port and the
	// same token.
	a := restStart(t, rw.repo)
	b := restStart(t, rw.repo)
	if a.port == b.port || a.token != b.token {
		t.Errorf("two servers: ports %s %s, same token %v", a.port, b.port, a.token == b.token)
	}
	for _, s := range []*restAPI{a, b} {
		if r := s.get(t, "/v1/memories"); r.status != http.StatusOK {
			t.Errorf("server on %s: %d", s.port, r.status)
		}
	}
}
