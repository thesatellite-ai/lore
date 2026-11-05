package rest

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/surface/docs"
)

// Server constants (spec 9.7, 12).
const (
	// DefaultPort is `lore api`'s port when --port is not given.
	DefaultPort = 7461
	// BindHost is the only address the server listens on in 0.1.18: there
	// is no flag for another one (SERVER SCOPE: 0.1.18 is the local
	// foundation; `lore serve` and remote access come in a later release).
	BindHost = "127.0.0.1"
	// MaxBodyBytes caps a request body (MaxFileSize, 4 MiB).
	MaxBodyBytes = 4 << 20
	// maxPort is the highest TCP port.
	maxPort = 65535

	// HeaderAgent names the agent acting for the machine's person; it is
	// recorded for attribution only and grants nothing.
	HeaderAgent = "X-Lore-Agent"
	// MaxAgentLen caps the X-Lore-Agent value.
	MaxAgentLen = 128

	headerAuth        = "Authorization"
	headerContentType = "Content-Type"
	headerNoSniff     = "X-Content-Type-Options"
	headerWWWAuth     = "WWW-Authenticate"
	bearerPrefix      = "Bearer "
	noSniff           = "nosniff"
	mediaJSON         = "application/json"

	// PathOpenAPI serves the OpenAPI document (fixed operations plus the
	// attached stores' kinds).
	PathOpenAPI = "/openapi.json"
	// PathSpec serves the registry and kinds for generic UIs (spec 9.8).
	PathSpec = op.RESTPrefix + "/spec"

	readHeaderTimeout = 10 * time.Second
	readTimeout       = 60 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// KindSource lists the dynamic kinds of the attached stores, for /v1/spec
// and the served /openapi.json. The engine implements it; nil means no
// kinds.
type KindSource interface {
	Kinds(ctx context.Context) ([]Kind, error)
}

// Kind is one dynamic kind as a generic UI sees it.
type Kind struct {
	Def schema.TableDef
	// Source is the name of the attached store that defines it.
	Source string
}

// Options configures a Server.
type Options struct {
	// Token is the bearer token every request must carry. Required: New
	// refuses an empty token, so the API can never start open (SERVER
	// SCOPE: local API is closed by default even on 127.0.0.1).
	Token string
	// Actor is the machine's person: every request runs as them (spec 9.7).
	Actor ids.ID[ids.Actor]
	// LoreVersion is reported by /v1/spec.
	LoreVersion string
	// Kinds may be nil.
	Kinds KindSource
}

// Server is the `lore api` HTTP surface. It holds no global state; build
// one per process with New.
type Server struct {
	handler http.Handler
}

// New builds the routes of every non-hidden operation in reg, all served
// through runner. It refuses (E_CONFIG_INVALID) an empty token or two
// operations whose routes the mux cannot tell apart.
func New(reg *op.Registry, runner op.Runner, opts Options) (*Server, error) {
	if opts.Token == "" {
		return nil, errs.New(errs.ConfigInvalid, "rest: no api token; refusing to serve an open API")
	}
	mux := http.NewServeMux()
	h := &handlers{reg: reg, runner: runner, opts: opts}
	if err := register(mux, "GET "+PathOpenAPI, http.HandlerFunc(h.openapi)); err != nil {
		return nil, err
	}
	if err := register(mux, "GET "+PathSpec, http.HandlerFunc(h.spec)); err != nil {
		return nil, err
	}
	for _, e := range reg.Entries() {
		rt, ok := op.RouteOf(e.Spec(), e.InputFields())
		if !ok {
			continue
		}
		if err := register(mux, rt.Method+" "+rt.Path, h.operation(e, rt)); err != nil {
			return nil, err
		}
		if bt, bulk := bulkRoute(e, rt); bulk && !served(mux, bt) {
			if err := register(mux, bt.Method+" "+bt.Path, h.operation(e, bt)); err != nil {
				return nil, err
			}
		}
	}
	if err := register(mux, "/", http.HandlerFunc(notFound)); err != nil {
		return nil, err
	}
	return &Server{handler: authenticate(opts.Token, limitBody(kindRoutes(mux)))}, nil // kindroutes.go: /v1/resources/<kind>
}

// register adds a pattern, turning the mux's conflict panic into an error.
func register(mux *http.ServeMux, pattern string, h http.Handler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errs.New(errs.ConfigInvalid, fmt.Sprintf("rest: route %s: %v", pattern, r))
		}
	}()
	mux.Handle(pattern, h)
	return nil
}

// Handler is the complete, authenticated handler (tests, embedding).
func (s *Server) Handler() http.Handler { return s.handler }

// Listen binds BindHost:port; port 0 picks a free port (read it from the
// listener's address).
func Listen(port int) (net.Listener, error) {
	if port < 0 || port > maxPort {
		return nil, errs.New(errs.InvalidInput, "--port must be 0 to "+strconv.Itoa(maxPort))
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", net.JoinHostPort(BindHost, strconv.Itoa(port)))
	if err != nil {
		return nil, errs.Wrap(errs.Unsupported, "rest: listen on port "+strconv.Itoa(port), err)
	}
	return ln, nil
}

// Serve runs the server on ln until ctx is done, then shuts down
// gracefully. It writes `lore api listening on http://127.0.0.1:<port>` and
// note to announce first, so `--port 0` prints the port it picked.
func (s *Server) Serve(ctx context.Context, ln net.Listener, announce io.Writer, note string) error {
	line := "lore api listening on http://" + ln.Addr().String()
	if note != "" {
		line += " (" + note + ")"
	}
	if _, err := io.WriteString(announce, line+"\n"); err != nil {
		_ = ln.Close()
		return errs.Wrap(errs.Internal, "rest: announce", err)
	}
	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	fresh := trackFreshConns(srv)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return errs.Wrap(errs.Internal, "rest: serve", err)
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	srv.RegisterOnShutdown(fresh.closeAll)
	if err := srv.Shutdown(sctx); err != nil {
		return errs.Wrap(errs.Internal, "rest: shutdown", err)
	}
	if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errs.Wrap(errs.Internal, "rest: serve", err)
	}
	return nil
}

// freshConns tracks the connections a client opened but has not sent a
// request on yet (http.StateNew).
//
// Why: http.Server.Shutdown waits for such a connection as if it were busy
// until it is 5 s old, so one pre-connected socket (a browser's speculative
// connect, or Go's own transport dialling a spare while an earlier
// connection frees up) made every `lore api` shutdown take the full
// shutdownTimeout and fail with E_INTERNAL. Nothing has been read from a
// fresh connection, so closing it at shutdown loses no request.
type freshConns struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// trackFreshConns installs the ConnState hook on srv and returns the set it
// keeps. Call it before srv.Serve.
func trackFreshConns(srv *http.Server) *freshConns {
	f := &freshConns{conns: map[net.Conn]struct{}{}}
	srv.ConnState = func(c net.Conn, st http.ConnState) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if st == http.StateNew {
			f.conns[c] = struct{}{}
			return
		}
		delete(f.conns, c)
	}
	return f
}

// closeAll closes every connection still waiting for its first request. It
// runs from Shutdown (RegisterOnShutdown), after the listeners are closed,
// so no new connection can join the set afterwards.
func (f *freshConns) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for c := range f.conns {
		_ = c.Close()
		delete(f.conns, c)
	}
}

// authenticate refuses every request without the right bearer token, before
// any route runs (closed by default).
//
// The comparison hashes both sides and compares the digests in constant
// time, so neither the token's bytes nor its length leak through timing.
func authenticate(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get(headerAuth), bearerPrefix)
		sum := sha256.Sum256([]byte(got))
		if !ok || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			w.Header().Set(headerWWWAuth, strings.TrimSpace(bearerPrefix))
			writeError(w, errs.New(errs.Unauthorized, "missing or wrong bearer token").
				WithHint("send Authorization: Bearer <the token in ~/.lore/api-token>"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limitBody caps every request body at MaxBodyBytes.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// notFound answers a path no route has.
func notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, errs.New(errs.NotFound, "no route "+r.Method+" "+r.URL.Path))
}

// StatusOf is the HTTP status of err: always from its class (O3, O5), so no
// code but E_INTERNAL can answer 500.
func StatusOf(err error) int {
	b := errs.EnvelopeOf(err).Error
	return errs.HTTPFor(b.Class, b.Exit)
}

// writeError writes the spec 9.4 error object under its class status.
func writeError(w http.ResponseWriter, err error) {
	// Envelope is a fixed struct of strings and ints; Marshal cannot fail.
	b, mErr := json.Marshal(errs.EnvelopeOf(err))
	if mErr != nil {
		b = []byte(`{"error":{"code":"E_INTERNAL","message":"encode error","exit":1,"class":"generic"}}`)
	}
	writeJSON(w, StatusOf(err), b)
}

// writeJSON writes a JSON body with the safety headers.
func writeJSON(w http.ResponseWriter, status int, body []byte) {
	h := w.Header()
	h.Set(headerContentType, mediaJSON)
	h.Set(headerNoSniff, noSniff)
	w.WriteHeader(status)
	// A failed write means the client went away; there is no one to tell.
	_, _ = w.Write(append(body, '\n'))
}

// bulkRoute is the collection-path twin of an edit or delete route whose
// id is optional (the bulk form --kind/--where, CLI_REFERENCE 3.2):
// DELETE /v1/resources?kind=host&where=… runs resource.delete without an
// id, so REST reaches every form the CLI has (ONE OPERATION SPEC; r3-batch,
// P2-RES-1). An operation whose id is required has no twin.
func bulkRoute(e op.Entry, rt op.Route) (op.Route, bool) {
	if rt.Method != http.MethodDelete && rt.Method != http.MethodPatch {
		return op.Route{}, false
	}
	base, ok := strings.CutSuffix(rt.Path, "/{"+docs.IDParam+"}")
	if !ok {
		return op.Route{}, false
	}
	for _, f := range e.InputFields() {
		if f.Name == docs.IDParam {
			return op.Route{Method: rt.Method, Path: base, Status: rt.Status}, !f.Required
		}
	}
	return op.Route{}, false
}

// served reports whether mux already has a route for rt's method and
// path (a registry operation keeps the path; the twin never shadows it).
func served(mux *http.ServeMux, rt op.Route) bool {
	r, err := http.NewRequest(rt.Method, rt.Path, nil)
	if err != nil {
		return true
	}
	_, pattern := mux.Handler(r)
	return pattern != "" && pattern != "/"
}
