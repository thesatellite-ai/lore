// Package setupweb is the local setup page (layer L6, ONB section 5): the
// one front end of onboarding. It serves the static files of a UI (ui.go:
// the one handed in, or the open-core DefaultUI) and the
// registry routes of the allowlisted operations (allow.go), built by
// surface/rest exactly as `lore api` builds them, behind a session check
// instead of the bearer check.
//
//	lore setup ─▶ Listen (127.0.0.1, random port, never 7461)
//	           ─▶ New: one-time token (32 bytes hex)
//	           ─▶ Serve: print "lore setup is open at …/setup/#t=<token>", open the browser
//	browser    ─▶ GET /setup/… (index.html for any client route) + /setup/assets/…
//	           ─▶ POST /setup/session {token} ─▶ HttpOnly cookie + CSRF value; the token is dead
//	           ─▶ /v1/… (allowlisted ops) with cookie, X-Lore-CSRF, Origin
//	stop       ◀─ POST /setup/close | idle minutes | ctx done (Ctrl-C)
//
// Security (ONB 5.3) lives in session.go; every response carries the CSP,
// nosniff, no-referrer and no-store headers (safetyHeaders).
package setupweb

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/surface/docs"
	"github.com/thesatellite-ai/lore/surface/rest"
)

// Server constants (ONB 5.1, 7.3).
const (
	// DefaultIdleMinutes is setup.idle_minutes when the config has none.
	DefaultIdleMinutes = 30
	// PagePath is the page's URL path; the token rides in its fragment.
	PagePath = "/setup/"
	// fragmentToken is the fragment key of the one-time token.
	fragmentToken = "#t="

	readHeaderTimeout = 10 * time.Second
	readTimeout       = 60 * time.Second
	idleConnTimeout   = 120 * time.Second
	shutdownTimeout   = 5 * time.Second
	// idleTick is how often the idle stop is checked.
	idleTick = 15 * time.Second
	// SlowRequest is the time past which an operation call is logged to
	// the terminal with its duration ("setup page: POST /v1/… 200 in
	// 1.4s"), so a page that feels stuck shows where the time went
	// without logging every fast call.
	SlowRequest = 500 * time.Millisecond
)

// Flow is the screen the page opens on.
type Flow string

// Flows (ONB 5.1).
const (
	// FlowHome is bare `lore setup` / `lore setup web`.
	FlowHome Flow = "home"
	// FlowJoin is `lore setup join <invite>`: the page opens on the join
	// flow with the invite text filled in.
	FlowJoin Flow = "join"
)

// Options configures a Page.
type Options struct {
	// Registry and Runner are the binary's; only allowlisted operations
	// are served.
	Registry *op.Registry
	Runner   op.Runner
	// Actor is the machine's person: every request runs as them.
	Actor       ids.ID[ids.Actor]
	LoreVersion string
	// IdleMinutes stops the server after that many minutes without a
	// request (0: DefaultIdleMinutes).
	IdleMinutes int
	// Flow and Invite pick the first screen (Invite only for FlowJoin).
	// The invite is handed to the page in the session response, never in
	// the URL.
	Flow   Flow
	Invite string
	// Setup preselects a setup by name (--setup); "" for none.
	Setup string
	// Here is the context of the folder the page was started in
	// (onboard.ResolveHere); the session answer carries it for the Home
	// banner (session.go). Zero state: not known, the banner shows nothing.
	Here onboard.Here
	// UI supplies the page's static files (ui.go); nil serves DefaultUI.
	UI UI
	// DebugLog is the debug log file (<lore home>/state/setup-debug.log,
	// debuglog.go); "" keeps the log in memory only.
	DebugLog string
	// Now is the clock (nil: time.Now); tests move it for the idle stop.
	Now func() time.Time
}

// Page is one run of the setup page server. Build it with New; it holds
// no global state.
type Page struct {
	opts    Options
	port    int
	origin  string // http://127.0.0.1:<port>
	host    string // 127.0.0.1:<port>
	token   string
	handler http.Handler
	routes  map[string]routeJSON
	debug   *debugLog // every /v1 request and its probe calls (debuglog.go)

	mu       sync.Mutex
	used     bool    // the one-time token was spent
	session  *secret // nil until the token is spent
	lastSeen time.Time

	closeOnce sync.Once
	closed    chan struct{}

	// slowLog receives the SlowRequest lines; set by Serve before it
	// serves (nil: not logged, e.g. Handler in tests).
	slowLog io.Writer
	logMu   sync.Mutex
}

// secret is the session's two random values (hex): the cookie and the
// CSRF header value.
type secret struct {
	cookie string
	csrf   string
}

// Listen binds 127.0.0.1:port (0: a free port). It refuses `lore api`'s
// port so both can run at once (ONB 5.1, 10.12). A --port that cannot be
// bound (taken by another program) names the port and the way out; the
// code stays rest.Listen's.
func Listen(port int) (net.Listener, error) {
	if port == rest.DefaultPort {
		return nil, errs.New(errs.InvalidInput, "--port "+strconv.Itoa(port)+" is lore api's port; pick another or 0 for a free one")
	}
	ln, err := rest.Listen(port)
	if err != nil && port != 0 && !errs.Has(err, errs.InvalidInput) {
		return nil, errs.Prefix("the setup page cannot listen on --port "+strconv.Itoa(port)+" (another program may hold it)", err).
			WithHint(hintFreePort)
	}
	return ln, err
}

// hintFreePort is the way out of a --port that cannot be bound.
const hintFreePort = "--port 0 picks a free one"

// writeTimeout is the server's WriteTimeout: it bounds the non-operation
// routes (session, assets, debug log). An operation's own run is not under
// it: the /v1 handler (surface/rest runUnbounded) lifts the deadline while
// the operation runs and gives the reply a fresh one, which reaches the
// connection only because every wrapper here unwraps (statusWriter,
// captureWriter). A var only so a test can shorten it.
var writeTimeout = 5 * time.Minute

// New builds the page server for ln (its port is the only Host and Origin
// accepted) with a fresh one-time token.
func New(ln net.Listener, opts Options) (*Page, error) {
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !tcp.IP.Equal(net.ParseIP(rest.BindHost)) {
		return nil, errs.New(errs.ConfigInvalid, "setupweb: listener is not on "+rest.BindHost)
	}
	if opts.Registry == nil || opts.Runner == nil {
		return nil, errs.New(errs.Internal, "setupweb: no registry or runner")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.IdleMinutes <= 0 {
		opts.IdleMinutes = DefaultIdleMinutes
	}
	if opts.Flow == "" {
		opts.Flow = FlowHome
	}
	tok, err := rest.NewToken()
	if err != nil {
		return nil, err
	}
	p := &Page{opts: opts, port: tcp.Port, token: tok, closed: make(chan struct{})}
	p.host = rest.BindHost + ":" + strconv.Itoa(p.port)
	p.origin = "http://" + p.host
	p.lastSeen = opts.Now()
	api, err := rest.New(opts.Registry, opts.Runner, rest.Options{
		Actor: opts.Actor, LoreVersion: opts.LoreVersion,
		Authenticate: p.requireSession, Allow: Allowed,
	})
	if err != nil {
		return nil, err
	}
	p.routes = map[string]routeJSON{}
	for name, rt := range rest.Routes(opts.Registry, Allowed) {
		r := routeJSON{Method: rt.Method, Path: rt.Path}
		if e, ok := opts.Registry.Lookup(name); ok {
			r.ID = docs.PathIDName(e.InputFields(), rt.Path)
		}
		p.routes[name] = r
	}
	p.debug = &debugLog{path: opts.DebugLog}
	p.handler = p.traced(p.build(api.Handler()))
	return p, nil
}

// routeJSON is one operation's route as the page reads it.
type routeJSON struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	// ID is the input field the path's {id} fills (docs.PathIDName: "id",
	// else the operation's first positional argument, e.g. setup.show's
	// name); omitted when the path has no {id}.
	ID string `json:"id,omitempty"`
}

// Port is the port the page listens on.
func (p *Page) Port() int { return p.port }

// URL is the page's address with the one-time token in the fragment (a
// fragment is never sent in a request line or a Referer, ONB 5.3).
func (p *Page) URL() string { return p.origin + PagePath + fragmentToken + p.token }

// Handler is the complete handler (tests).
func (p *Page) Handler() http.Handler { return p.handler }

// Done is closed when the page asked to close (POST /setup/close).
func (p *Page) Done() <-chan struct{} { return p.closed }

// close ends the run; safe to call more than once.
func (p *Page) close() { p.closeOnce.Do(func() { close(p.closed) }) }

// touch records activity for the idle stop.
func (p *Page) touch() {
	p.mu.Lock()
	p.lastSeen = p.opts.Now()
	p.mu.Unlock()
}

// idle reports whether the page has been idle for IdleMinutes.
func (p *Page) idle() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.opts.Now().Sub(p.lastSeen) >= time.Duration(p.opts.IdleMinutes)*time.Minute
}

// Announce is the one line `lore setup` prints (ONB 1.4).
func (p *Page) Announce() string {
	return "lore setup is open at " + p.URL() + "   (only this machine; closes after " +
		strconv.Itoa(p.opts.IdleMinutes) + " idle minutes)"
}

// Serve prints Announce to announce, calls open with the URL (nil: do not
// open a browser), and serves on ln until ctx is done, the page is closed,
// or it is idle for IdleMinutes. The reason it stopped is printed as one
// more line.
func (p *Page) Serve(ctx context.Context, ln net.Listener, announce io.Writer, open func(ctx context.Context, url string) error) error {
	if _, err := io.WriteString(announce, p.Announce()+"\n"); err != nil {
		_ = ln.Close()
		return errs.Wrap(errs.Internal, "setupweb: announce", err)
	}
	p.slowLog = announce
	srv := &http.Server{
		Handler:           p.timed(p.handler),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleConnTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	if open != nil {
		if err := open(ctx, p.URL()); err != nil {
			// The URL line above is enough to continue by hand.
			_, _ = io.WriteString(announce, "could not open a browser ("+err.Error()+"); open the address above\n")
		}
	}
	tick := time.NewTicker(idleTick)
	defer tick.Stop()
	why := ""
loop:
	for {
		select {
		case err := <-done:
			return errs.Wrap(errs.Internal, "setupweb: serve", err)
		case <-ctx.Done():
			why = "interrupted"
			break loop
		case <-p.closed:
			why = "closed from the page"
			break loop
		case <-tick.C:
			if p.idle() {
				why = "idle for " + strconv.Itoa(p.opts.IdleMinutes) + " minutes"
				break loop
			}
		}
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close() // a request still running past the timeout is cut
	}
	if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errs.Wrap(errs.Internal, "setupweb: serve", err)
	}
	_, _ = io.WriteString(announce, "lore setup stopped ("+why+")\n")
	return nil
}

// timed logs every /v1 operation call slower than SlowRequest to
// p.slowLog: method, path, status and duration.
func (p *Page) timed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := p.opts.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		took := p.opts.Now().Sub(start)
		if p.slowLog == nil || took < SlowRequest || !strings.HasPrefix(r.URL.Path, pathV1) {
			return
		}
		p.logMu.Lock()
		defer p.logMu.Unlock()
		_, _ = io.WriteString(p.slowLog, "setup page: "+r.Method+" "+r.URL.Path+" "+strconv.Itoa(sw.status)+" in "+took.Round(time.Millisecond).String()+"\n")
	})
}

// statusWriter remembers the status code a handler wrote.
type statusWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader records code and passes it on.
func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the real writer (flush, deadlines).
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
