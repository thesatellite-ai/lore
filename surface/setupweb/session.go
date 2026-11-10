package setupweb

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/surface/rest"
)

// Session and header constants (ONB 5.3).
const (
	// CookieName is the session cookie.
	CookieName = "lore_setup"
	// HeaderCSRF carries the per-session CSRF value on every mutating request.
	HeaderCSRF = "X-Lore-CSRF"

	// CSP is the page's Content-Security-Policy: ONB 5.3's policy
	// tightened for a built UI (ui.go). Every script and stylesheet is a
	// file from this server ('self'), so there is no 'unsafe-inline' at
	// all: the old hand-written page needed it for the Tailwind browser
	// build's <style> element, a built bundle does not (React sets styles
	// through the CSSOM, which style-src does not govern). font-src is
	// 'self' for bundled fonts; form-action 'none' because the page posts
	// JSON with fetch, never a form.
	CSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

	// PathSession is POST {token} (start) and GET (reload: the CSRF value
	// again, cookie required).
	PathSession = "/setup/session"
	// PathClose stops the server ([Close] on the page).
	PathClose = "/setup/close"
	// pathAssets is the prefix of the embedded static files.
	pathAssets = "/setup/assets/"
	// pathV1 is the prefix of the registry routes.
	pathV1 = "/v1/"

	headerCSP          = "Content-Security-Policy"
	headerNoSniff      = "X-Content-Type-Options"
	headerReferrer     = "Referrer-Policy"
	headerCache        = "Cache-Control"
	headerOrigin       = "Origin"
	headerContentType  = "Content-Type"
	valueNoSniff       = "nosniff"
	valueNoReferrer    = "no-referrer"
	valueNoStore       = "no-store"
	mediaJSON          = "application/json"
	maxSessionBodySize = 4 << 10

	// msgTokenUsed is the design's text for a second use (ONB 5.3).
	msgTokenUsed  = "this setup link was already used; run lore setup again for a new one"
	msgTokenWrong = "wrong setup token; open the address lore setup printed"
	msgNoSession  = "no setup session; open the address lore setup printed"
	hintNewLink   = "run lore setup again for a new link"
)

// build wraps every route: the safety headers on every answer, then the
// Host check before any routing (DNS rebinding, ONB 5.3), then the mux.
func (p *Page) build(api http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+pathAssets+"{file...}", p.serveAsset)
	mux.HandleFunc("GET "+PagePath+"{path...}", p.serveIndex) // SPA fallback (assets.go)
	mux.HandleFunc("POST "+PathSession, p.startSession)
	mux.HandleFunc("GET "+PathSession, p.resumeSession)
	mux.Handle("POST "+PathClose, p.requireSession(http.HandlerFunc(p.closePage)))
	// the debug log's entries (debuglog.go), session only
	mux.Handle("GET "+PathDebug, p.requireSession(http.HandlerFunc(p.serveDebug)))
	mux.Handle(pathV1, api) // rest: allowlisted ops only, each behind requireSession
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rest.WriteError(w, errs.New(errs.NotFound, "no route "+r.Method+" "+r.URL.Path))
	})
	return safetyHeaders(p.checkHost(mux))
}

// checkHost refuses (400) a request whose Host is not exactly
// 127.0.0.1:<port>: a page on another site that re-points its own name at
// 127.0.0.1 (DNS rebinding) sends its own name as Host and is stopped here,
// before any route runs.
func (p *Page) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != p.host {
			rest.WriteError(w, errs.New(errs.InvalidInput, "wrong Host "+quote(r.Host)+"; the setup page answers only "+p.host))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// safetyHeaders sets the ONB 5.3 headers on every response, errors included.
func safetyHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set(headerCSP, CSP)
		h.Set(headerNoSniff, valueNoSniff)
		h.Set(headerReferrer, valueNoReferrer)
		h.Set(headerCache, valueNoStore)
		next.ServeHTTP(w, r)
	})
}

// requireSession is the page's authenticator (rest.Options.Authenticate,
// ONB 10.5): a valid session cookie on every request, and on every
// request that is not GET/HEAD also a JSON body, the CSRF header and the
// page's own Origin. It refuses before the route runs.
func (p *Page) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := p.sessionOf(r)
		if err != nil {
			rest.WriteError(w, err)
			return
		}
		if mutating(r.Method) {
			if err := p.checkMutation(r, s); err != nil {
				rest.WriteError(w, err)
				return
			}
		}
		p.touch()
		// The page's own calls (op.Page): machine.reveal runs for them only.
		next.ServeHTTP(w, r.WithContext(op.WithPage(r.Context())))
	})
}

// mutating reports whether method may change something: every method but
// GET and HEAD. Registry GET routes are list/show only.
func mutating(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}

// sessionOf returns the session whose cookie r carries.
func (p *Page) sessionOf(r *http.Request) (secret, error) {
	p.mu.Lock()
	s := p.session
	p.mu.Unlock()
	c, err := r.Cookie(CookieName)
	if s == nil || err != nil || !equal(c.Value, s.cookie) {
		return secret{}, errs.New(errs.Unauthorized, msgNoSession).WithHint(hintNewLink)
	}
	return *s, nil
}

// checkMutation is the CSRF rule of ONB 5.3: JSON content type, the
// session's X-Lore-CSRF value, and Origin == http://127.0.0.1:<port>.
func (p *Page) checkMutation(r *http.Request, s secret) error {
	if err := checkJSON(r); err != nil {
		return err
	}
	if !equal(r.Header.Get(HeaderCSRF), s.csrf) {
		return errs.New(errs.Unauthorized, "missing or wrong "+HeaderCSRF+" header").WithHint("reload the setup page")
	}
	return p.checkOrigin(r)
}

// checkJSON refuses a body that is not declared application/json (a
// cross-site form can only send form or text types).
func checkJSON(r *http.Request) error {
	mt, _, err := mime.ParseMediaType(r.Header.Get(headerContentType))
	if err != nil || mt != mediaJSON {
		return errs.New(errs.InvalidInput, "a setup request must send Content-Type: "+mediaJSON)
	}
	return nil
}

// checkOrigin refuses a mutating request from any other origin, or with
// none (every browser sends Origin on a POST).
func (p *Page) checkOrigin(r *http.Request) error {
	if o := r.Header.Get(headerOrigin); o != p.origin {
		return errs.New(errs.Unauthorized, "wrong Origin "+quote(o)+"; the setup page accepts only "+p.origin)
	}
	return nil
}

// sessionIn is the body of POST /setup/session.
type sessionIn struct {
	Token string `json:"token"`
}

// sessionOut is what the page gets with the cookie.
type sessionOut struct {
	CSRF    string               `json:"csrf"`
	Flow    Flow                 `json:"flow"`
	Invite  string               `json:"invite,omitempty"`
	Setup   string               `json:"setup,omitempty"`
	Version string               `json:"version"`
	Idle    int                  `json:"idle_minutes"`
	Routes  map[string]routeJSON `json:"routes"`
	// Build is the binary's build facts for the page's About popover.
	Build buildJSON `json:"build"`
	// Here is the folder the page was started in (onboard.ResolveHere):
	// which setup covers it, or the setup to create from it; null when
	// not known (the banner then shows nothing).
	Here *onboard.Here `json:"here"`
}

// startSession spends the one-time token: right token, first use → the
// session cookie and CSRF value; any later use → E_UNAUTHORIZED with the
// design's text, whatever the token.
func (p *Page) startSession(w http.ResponseWriter, r *http.Request) {
	if err := checkJSON(r); err != nil {
		rest.WriteError(w, err)
		return
	}
	if err := p.checkOrigin(r); err != nil {
		rest.WriteError(w, err)
		return
	}
	var in sessionIn
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSessionBodySize))
	if err != nil || json.Unmarshal(raw, &in) != nil {
		rest.WriteError(w, errs.New(errs.InvalidInput, `the body must be {"token": "…"}`))
		return
	}
	p.mu.Lock()
	if p.used {
		p.mu.Unlock()
		rest.WriteError(w, errs.New(errs.Unauthorized, msgTokenUsed))
		return
	}
	if !equal(in.Token, p.token) {
		p.mu.Unlock()
		rest.WriteError(w, errs.New(errs.Unauthorized, msgTokenWrong).WithHint(hintNewLink))
		return
	}
	s, err := newSecret()
	if err != nil {
		p.mu.Unlock()
		rest.WriteError(w, err)
		return
	}
	p.used, p.session = true, &s
	p.lastSeen = p.opts.Now()
	p.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: s.cookie, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	p.writeSession(w, s)
}

// resumeSession answers a reload: the page lost its CSRF value but still
// has the cookie. Another site cannot read this answer (no CORS headers,
// and the cookie is SameSite=Strict), and a rebound name fails checkHost.
func (p *Page) resumeSession(w http.ResponseWriter, r *http.Request) {
	s, err := p.sessionOf(r)
	if err != nil {
		rest.WriteError(w, err)
		return
	}
	p.touch()
	p.writeSession(w, s)
}

// writeSession writes the session response.
func (p *Page) writeSession(w http.ResponseWriter, s secret) {
	out := sessionOut{CSRF: s.csrf, Flow: p.opts.Flow, Invite: p.opts.Invite, Setup: p.opts.Setup,
		Version: p.opts.LoreVersion, Idle: p.opts.IdleMinutes, Routes: p.routes, Build: buildFacts()}
	if p.opts.Here.State != "" {
		h := p.opts.Here
		out.Here = &h
	}
	writeOK(w, out)
}

// closeOut is the answer of POST /setup/close.
type closeOut struct {
	Closed bool `json:"closed"`
}

// closePage stops the server after answering.
func (p *Page) closePage(w http.ResponseWriter, _ *http.Request) {
	writeOK(w, closeOut{Closed: true})
	p.close()
}

// writeOK writes a JSON 200 answer.
func writeOK[T sessionOut | closeOut](w http.ResponseWriter, v T) {
	b, err := json.Marshal(v)
	if err != nil {
		rest.WriteError(w, errs.Wrap(errs.Internal, "setupweb: encode", err))
		return
	}
	w.Header().Set(headerContentType, mediaJSON)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(b, '\n')) // a failed write means the page went away
}

// newSecret mints the session's cookie and CSRF values.
func newSecret() (secret, error) {
	c, err := rest.NewToken()
	if err != nil {
		return secret{}, err
	}
	x, err := rest.NewToken()
	if err != nil {
		return secret{}, err
	}
	return secret{cookie: c, csrf: x}, nil
}

// equal compares two secrets in constant time over their digests, so
// neither the bytes nor the length leak through timing; "" never matches.
func equal(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	a, b := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// quote shows a header value in an error, cut to a sane length.
func quote(v string) string {
	const maxShown = 80
	if len(v) > maxShown {
		v = v[:maxShown] + "…"
	}
	if strings.TrimSpace(v) == "" {
		return `""`
	}
	return `"` + v + `"`
}
