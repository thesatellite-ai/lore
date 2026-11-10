// Package ghapi is a small read-only GitHub REST client for the setup
// page's account discovery (ONBOARDING_DESIGN 4.2/4.3, owner request
// 2026-11-06 "figure out all automatically … and also fetch the email").
//
//	wire ─▶ probe.GHToken(user) ─▶ Token (memory only)
//	     ─▶ Client.User / Emails / Repos (Authorization header, BaseURL only)
//	     ─▶ Client.Avatar (no token: the avatar host is another origin)
//
// Secrets: a Token holds a gh OAuth token read from `gh auth token --user
// X` stdout. It never leaves this package except as an Authorization
// header sent to BaseURL: every formatter (%v, %s, %#v, JSON, slog) prints
// tokenRedacted, and every error this package returns is built from fixed
// text plus the HTTP status, never from a response body, a header or a
// url that could echo the token. Nothing is cached on disk.
package ghapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is api.github.com. EnvBaseURL overrides it: TEST ONLY
// (the setup page's headless checks point it at a local fake API); a real
// user never sets it.
const (
	DefaultBaseURL = "https://api.github.com"
	EnvBaseURL     = "LORE_GITHUB_API"
)

// Limits: each request is bounded so discovery never hangs the page
// (5 s, as the ssh probes), and bodies are capped so a broken or hostile
// server cannot fill memory.
const (
	DefaultTimeout = 5 * time.Second
	maxBody        = 2 << 20
	maxAvatar      = 128 << 10
	// avatarSize asks GitHub for a 64 px avatar (the card shows 32 px at 2x).
	avatarSize = "64"
	// reposPerPage is one page of /user/repos: enough to find a store repo
	// among recently updated ones without paging through hundreds.
	reposPerPage = "100"
	apiVersion   = "2022-11-28"
	userAgent    = "lore-setup"
)

// Scope names read from X-OAuth-Scopes: user:email or the wider user
// scope lets /user/emails answer.
const (
	headerScopes = "X-OAuth-Scopes"
	scopeUser    = "user"
	scopeEmail   = "user:email"
	// ScopeNeeded is the scope the page asks gh to add.
	ScopeNeeded = scopeEmail
)

// tokenRedacted is what every formatter prints for a Token.
const tokenRedacted = "[redacted]"

// Token is a GitHub token kept in memory only. The zero Token is empty.
type Token struct{ v string }

// NewToken wraps s (surrounding space trimmed). ok is false when s is
// empty or holds whitespace or control characters inside (not a token).
func NewToken(s string) (Token, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.IndexFunc(s, func(r rune) bool { return r <= ' ' || r == 0x7f }) >= 0 {
		return Token{}, false
	}
	return Token{v: s}, true
}

// Empty reports a zero Token.
func (t Token) Empty() bool { return t.v == "" }

// String, GoString, MarshalJSON, MarshalText and LogValue never print the
// value: a Token passed to fmt, json, a template or slog by mistake
// prints tokenRedacted.
func (t Token) String() string               { return tokenRedacted }
func (t Token) GoString() string             { return tokenRedacted }
func (t Token) MarshalJSON() ([]byte, error) { return json.Marshal(tokenRedacted) }
func (t Token) MarshalText() ([]byte, error) { return []byte(tokenRedacted), nil }
func (t Token) LogValue() slog.Value         { return slog.StringValue(tokenRedacted) }
func (t Token) authorization() string        { return "Bearer " + t.v }

var _ slog.LogValuer = Token{}

// Errors. Fixed text only; Status carries the HTTP status when there was one.
var (
	ErrNoToken     = errors.New("ghapi: no token")
	ErrUnreachable = errors.New("ghapi: GitHub API unreachable")
	ErrBadResponse = errors.New("ghapi: GitHub API answer does not parse")
)

// StatusError is a non-2xx answer: the status only, never the body.
type StatusError struct{ Status int }

func (e *StatusError) Error() string {
	return "ghapi: GitHub API answered HTTP " + strconv.Itoa(e.Status)
}

// StatusOf is the HTTP status of err, 0 when it is not a StatusError.
func StatusOf(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status
	}
	return 0
}

// Client calls the GitHub REST API.
type Client struct {
	// BaseURL is the API root ("" means DefaultBaseURL). The token is sent
	// only to this origin.
	BaseURL string
	// HTTP is the client ("" means one with DefaultTimeout). Redirects to
	// another origin drop the Authorization header (net/http's rule).
	HTTP *http.Client
	// Observe, when set, is told about every request after it ends (the
	// setup page's trace: wire records it with probe.Record). nil = silent.
	Observe func(ctx context.Context, c Call)
}

// Call is one finished request for Observe. It carries no token: URL is
// the request url (the token only ever travels in a header) and Err is
// one of this package's fixed-text errors.
type Call struct {
	Method string
	URL    string
	Status int // 0 when no answer came
	Took   time.Duration
	Start  time.Time
	Err    error
}

// observe reports one request to c.Observe.
func (c Client) observe(ctx context.Context, method, u string, start time.Time, status int, err error) {
	if c.Observe != nil {
		c.Observe(ctx, Call{Method: method, URL: u, Status: status, Took: time.Since(start), Start: start, Err: err})
	}
}

func (c Client) base() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: DefaultTimeout}
}

// User is the /user answer's fields discovery reads.
type User struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"` // the public email; "" when hidden
	AvatarURL string `json:"avatar_url"`
}

// NoreplyEmail is the address GitHub uses for web commits when the real
// one is private: <id>+<login>@users.noreply.github.com.
func (u User) NoreplyEmail() string {
	if u.ID == 0 || u.Login == "" {
		return ""
	}
	return strconv.FormatInt(u.ID, 10) + "+" + u.Login + "@users.noreply.github.com"
}

// UserResult is User plus the token's scopes (X-OAuth-Scopes; Known is
// false for a token that sends no such header, a fine-grained one).
type UserResult struct {
	User        User
	Scopes      []string
	ScopesKnown bool
}

// CanReadEmails reports whether the scopes allow /user/emails; true when
// the scopes are unknown (the call itself then tells).
func (r UserResult) CanReadEmails() bool {
	if !r.ScopesKnown {
		return true
	}
	for _, s := range r.Scopes {
		if s == scopeUser || s == scopeEmail {
			return true
		}
	}
	return false
}

// User reads /user with tok.
func (c Client) User(ctx context.Context, tok Token) (UserResult, error) {
	u, h, err := get[User](ctx, c, tok, "/user")
	if err != nil {
		return UserResult{}, err
	}
	r := UserResult{User: u}
	if v, ok := h[http.CanonicalHeaderKey(headerScopes)]; ok {
		r.ScopesKnown = true
		for _, s := range strings.Split(strings.Join(v, ","), ",") {
			if s = strings.TrimSpace(s); s != "" {
				r.Scopes = append(r.Scopes, s)
			}
		}
	}
	return r, nil
}

// Email is one /user/emails row.
type Email struct {
	Email      string `json:"email"`
	Primary    bool   `json:"primary"`
	Verified   bool   `json:"verified"`
	Visibility string `json:"visibility,omitempty"`
}

// Emails reads /user/emails (needs user:email; GitHub answers 404 or 403
// without it, see StatusOf).
func (c Client) Emails(ctx context.Context, tok Token) ([]Email, error) {
	es, _, err := get[[]Email](ctx, c, tok, "/user/emails")
	return es, err
}

// Repo is one /user/repos row.
type Repo struct {
	FullName string `json:"full_name"`
	Name     string `json:"name"`
	SSHURL   string `json:"ssh_url"`
	Private  bool   `json:"private"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// Repos reads one page of /user/repos the token can see, most recently
// updated first.
func (c Client) Repos(ctx context.Context, tok Token) ([]Repo, error) {
	q := url.Values{"per_page": {reposPerPage}, "sort": {"updated"}, "affiliation": {"owner,collaborator,organization_member"}}
	rs, _, err := get[[]Repo](ctx, c, tok, "/user/repos?"+q.Encode())
	return rs, err
}

// Avatar fetches avatarURL (no token: it is another origin) at 64 px and
// returns it as a data: URI the page can show under its img-src 'self'
// data: CSP. "" with an error when it is not an image or too big.
func (c Client) Avatar(ctx context.Context, avatarURL string) (string, error) {
	u, err := url.Parse(avatarURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", ErrBadResponse
	}
	q := u.Query()
	q.Set("s", avatarSize)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", ErrBadResponse
	}
	req.Header.Set("User-Agent", userAgent)
	start := time.Now()
	resp, err := c.http().Do(req)
	if err != nil {
		c.observe(ctx, http.MethodGet, u.String(), start, 0, ErrUnreachable)
		return "", ErrUnreachable
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		se := &StatusError{Status: resp.StatusCode}
		c.observe(ctx, http.MethodGet, u.String(), start, resp.StatusCode, se)
		return "", se
	}
	c.observe(ctx, http.MethodGet, u.String(), start, resp.StatusCode, nil)
	ct := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if !strings.HasPrefix(ct, "image/") || strings.ContainsAny(ct, " ,\"") {
		return "", ErrBadResponse
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatar+1))
	if err != nil || len(b) > maxAvatar {
		return "", ErrBadResponse
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}

// get GETs base+path with tok and decodes the JSON answer as T. It also
// returns the response headers. Errors never carry the body or the url.
func get[T any](ctx context.Context, c Client, tok Token, path string) (T, http.Header, error) {
	var out T
	if tok.Empty() {
		return out, nil, ErrNoToken
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+path, nil)
	if err != nil {
		return out, nil, ErrBadResponse
	}
	req.Header.Set("Authorization", tok.authorization())
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	start := time.Now()
	resp, err := c.http().Do(req)
	if err != nil {
		c.observe(ctx, http.MethodGet, c.base()+path, start, 0, ErrUnreachable)
		return out, nil, ErrUnreachable // fixed text: err may hold the url
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		se := &StatusError{Status: resp.StatusCode}
		c.observe(ctx, http.MethodGet, c.base()+path, start, resp.StatusCode, se)
		return out, nil, se
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil {
		c.observe(ctx, http.MethodGet, c.base()+path, start, resp.StatusCode, ErrBadResponse)
		return out, nil, ErrBadResponse
	}
	c.observe(ctx, http.MethodGet, c.base()+path, start, resp.StatusCode, nil)
	return out, resp.Header, nil
}
