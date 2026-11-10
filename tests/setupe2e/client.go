//go:build setupe2e

package setupe2e

// client.go — the setup page's HTTP calls, made the way the page makes
// them: POST /setup/session {token} with Origin →
// HttpOnly cookie + CSRF value + the route table; then every operation
// through the route the session lists, mutating ones with a JSON body,
// X-Lore-CSRF and Origin.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Route is one operation's route as the session lists it.
type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	ID     string `json:"id,omitempty"`
}

type sessionOut struct {
	CSRF   string           `json:"csrf"`
	Routes map[string]Route `json:"routes"`
}

// Client is one page session.
type Client struct {
	origin string
	csrf   string
	routes map[string]Route
	hc     *http.Client
	world  *World
}

// APIError is a non-2xx answer: the spec 9.4 envelope.
type APIError struct {
	Status int
	Body   errs.Body
}

func (e *APIError) Error() string {
	return fmt.Sprintf("HTTP %d %s: %s (hint: %s)", e.Status, e.Body.Code, e.Body.Message, e.Body.Hint)
}

// NewClient spends the world's one-time token.
func NewClient(ctx context.Context, w *World) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("cookie jar: %w", err)
	}
	c := &Client{world: w, origin: w.Origin(), hc: &http.Client{Jar: jar, Timeout: 3 * time.Minute}}
	var s sessionOut
	tok := map[string]string{"token": w.Token()}
	if err := send(ctx, c, http.MethodPost, "/setup/session", &tok, &s); err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	if s.CSRF == "" || len(s.Routes) == 0 {
		return nil, fmt.Errorf("session: no csrf or routes")
	}
	c.csrf, c.routes = s.CSRF, s.Routes
	return c, nil
}

// Call runs one allowlisted operation; in is a JSON-tagged struct (or
// map), out receives the answer.
func Call[In, Out any](ctx context.Context, c *Client, op string, in In) (Out, error) {
	var out Out
	rt, ok := c.routes[op]
	if !ok {
		return out, fmt.Errorf("%s is not in the session's routes", op)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return out, fmt.Errorf("%s: encode: %w", op, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return out, fmt.Errorf("%s: input must be an object: %w", op, err)
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
		return out, send[map[string]json.RawMessage](ctx, c, http.MethodGet, path, nil, &out)
	}
	return out, send(ctx, c, rt.Method, path, &fields, &out)
}

// send sends one request. body nil = no body (GET).
func send[B, O any](ctx context.Context, c *Client, method, path string, body *B, out *O) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, rd)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", c.origin)
		if c.csrf != "" {
			req.Header.Set("X-Lore-CSRF", c.csrf)
		}
	}
	res, err := c.hc.Do(req)
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
		return &APIError{Status: res.StatusCode, Body: env.Error}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: decode %T: %w: %s", method, path, out, err, raw)
	}
	return nil
}
