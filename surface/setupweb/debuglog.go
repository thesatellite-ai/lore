package setupweb

// debuglog.go — the page's debug log (owner 2026-11-06: "an easy way to
// debug on every step"). Every /v1 request runs with a probe.Trace in its
// context, so each child program and HTTP call lore makes for it is
// recorded (adapters/probe/trace.go: command, exit, duration, trimmed
// output, all redacted). One entry per request then goes to:
//
//	a ring of the last debugRing entries ─▶ GET /setup/debug (session only) ─▶ Details panels + Debug log drawer
//	<lore home>/state/setup-debug.log    ─▶ one JSON line per entry, rotated debugLogMax × debugLogKeep
//
// The request body is kept as a short redacted excerpt (answers are names,
// emails, paths); every text passes the trace's redactor, so a token the
// op registered with probe.Secret never reaches the ring or the file.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/probe"
)

// Debug log constants.
const (
	// PathDebug is GET: the session's debug entries (?since=<seq>).
	PathDebug = "/setup/debug"
	// HeaderDebugSeq names the debug entry of a /v1 response, so the page
	// can open the calls of exactly that request.
	HeaderDebugSeq = "X-Lore-Debug-Seq"
	// DebugJobMethod is the Method of an entry that records a background
	// job (probe.StartJob) rather than a request; its Path is the job's
	// name ("discovery job") and its Op the operation that started it.
	DebugJobMethod = "JOB"
	// DebugLogFile is the log's name under <lore home>/state.
	DebugLogFile = "setup-debug.log"
	// debugRing is how many entries the page can read back.
	debugRing = 300
	// debugLogMax is the size past which the file rotates (.1, .2).
	debugLogMax = 1 << 20
	// debugLogKeep is how many files are kept, the live one included.
	debugLogKeep = 3
	// debugBodyMax is the most request-body bytes read for the excerpt
	// (the body is still passed on whole).
	debugBodyMax = 64 << 10
	// debugExcerpt is the most request-body bytes an entry keeps.
	debugExcerpt = 1024
	// debugErrBody is the most error-response bytes read for code+message.
	debugErrBody = 8 << 10
	// debugFileMode keeps the log readable by its owner only.
	debugFileMode = 0o600
	debugDirMode  = 0o700
)

// DebugEntry is one /v1 request with the calls it made.
type DebugEntry struct {
	Seq    int       `json:"seq"`
	At     time.Time `json:"at"`
	Method string    `json:"method"`
	Path   string    `json:"path"`
	Op     string    `json:"op,omitempty"`
	// Setup and Step are read from the request body when it names them.
	Setup   string       `json:"setup,omitempty"`
	Step    string       `json:"step,omitempty"`
	Status  int          `json:"status"`
	TookMS  int64        `json:"took_ms"`
	Code    string       `json:"code,omitempty"`
	Message string       `json:"message,omitempty"`
	Body    string       `json:"body,omitempty"`
	Calls   []probe.Call `json:"calls"`
	Dropped int          `json:"dropped_calls,omitempty"`
}

// debugOut is GET /setup/debug's answer.
type debugOut struct {
	Version string       `json:"version"`
	OS      string       `json:"os"`
	Arch    string       `json:"arch"`
	Go      string       `json:"go"`
	LogFile string       `json:"log_file,omitempty"`
	Entries []DebugEntry `json:"entries"`
}

// debugLog is the ring and the file. The zero path writes no file.
type debugLog struct {
	path string
	mu   sync.Mutex
	seq  int
	ring []DebugEntry
}

// add stores e (Seq already set) in the ring and appends it to the file.
func (d *debugLog) add(e DebugEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ring = append(d.ring, e)
	if over := len(d.ring) - debugRing; over > 0 {
		d.ring = append([]DebugEntry(nil), d.ring[over:]...)
	}
	if d.path == "" {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	// The log is a debugging aid: a disk that refuses it must never fail
	// the request it describes.
	_ = appendRotated(d.path, append(line, '\n'))
}

// next is the next entry number.
func (d *debugLog) next() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	return d.seq
}

// since is a copy of the entries after seq.
func (d *debugLog) since(seq int) []DebugEntry {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []DebugEntry{}
	for _, e := range d.ring {
		if e.Seq > seq {
			out = append(out, e)
		}
	}
	return out
}

// appendRotated appends line to path, first shifting path → path.1 →
// path.2 (the oldest dropped) when path would pass debugLogMax.
func appendRotated(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), debugDirMode); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil && fi.Size()+int64(len(line)) > debugLogMax {
		for i := debugLogKeep - 1; i >= 1; i-- {
			from := path
			if i > 1 {
				from = path + "." + strconv.Itoa(i-1)
			}
			_ = os.Rename(from, path+"."+strconv.Itoa(i)) // a missing older file is fine
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, debugFileMode)
	if err != nil {
		return err
	}
	_, werr := f.Write(line)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// traced records every /v1 request: a probe.Trace in its context, its op,
// status, duration, error code and message, and a redacted body excerpt.
func (p *Page) traced(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, pathV1) {
			next.ServeHTTP(w, r)
			return
		}
		start := p.opts.Now()
		seq := p.debug.next()
		ctx, tr := probe.WithTrace(r.Context())
		op := p.opOf(r.Method, r.URL.Path)
		ctx = probe.WithJobSink(ctx, func(j probe.JobRecord) { p.debug.add(jobEntry(p.debug.next(), op, j)) })
		var body []byte
		if r.Body != nil {
			head, err := io.ReadAll(io.LimitReader(r.Body, debugBodyMax))
			if err == nil {
				body = head
				r.Body = readCloser{Reader: io.MultiReader(bytes.NewReader(head), r.Body), Closer: r.Body}
			}
		}
		w.Header().Set(HeaderDebugSeq, strconv.Itoa(seq))
		cw := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(cw, r.WithContext(ctx))
		e := DebugEntry{Seq: seq, At: start, Method: r.Method, Path: r.URL.Path, Op: op,
			Status: cw.status, TookMS: p.opts.Now().Sub(start).Milliseconds(), Calls: tr.Calls(), Dropped: tr.Dropped()}
		e.Setup, e.Step = bodyNames(body)
		e.Body = excerpt(tr.Redact(string(body)))
		if cw.status >= http.StatusBadRequest {
			e.Code, e.Message = errorOf(cw.errBody.Bytes())
			e.Message = tr.Redact(e.Message)
		}
		p.debug.add(e)
	})
}

// jobEntry is the debug entry of a finished background job started by op:
// its own entry (it ends after the request's), status 500 when it failed.
func jobEntry(seq int, op string, j probe.JobRecord) DebugEntry {
	e := DebugEntry{Seq: seq, At: j.Start, Method: DebugJobMethod, Path: j.Name, Op: op, Status: http.StatusOK,
		TookMS: j.Took.Milliseconds(), Calls: j.Calls, Dropped: j.Dropped}
	if j.Err != "" {
		e.Status, e.Message = http.StatusInternalServerError, j.Err
	}
	if e.Calls == nil {
		e.Calls = []probe.Call{}
	}
	return e
}

// serveDebug answers GET /setup/debug[?since=N] (behind requireSession).
func (p *Page) serveDebug(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.Atoi(r.URL.Query().Get("since")) // absent or bad: everything
	out := debugOut{Version: p.opts.LoreVersion, OS: runtime.GOOS, Arch: runtime.GOARCH, Go: runtime.Version(),
		LogFile: p.debug.path, Entries: p.debug.since(since)}
	b, err := json.Marshal(out)
	if err != nil {
		http.Error(w, "encode", http.StatusInternalServerError)
		return
	}
	w.Header().Set(headerContentType, mediaJSON)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(b, '\n')) // a failed write means the page went away
}

// opOf is the operation whose route matches method and path ("" = none).
func (p *Page) opOf(method, path string) string {
	for name, rt := range p.routes {
		if rt.Method == method && routeMatch(rt.Path, path) {
			return name
		}
	}
	return ""
}

// routeMatch compares a route template (/v1/setups/{id}) with a path.
func routeMatch(tmpl, path string) bool {
	a, b := strings.Split(strings.Trim(tmpl, "/"), "/"), strings.Split(strings.Trim(path, "/"), "/")
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] && !(strings.HasPrefix(a[i], "{") && strings.HasSuffix(a[i], "}")) {
			return false
		}
	}
	return true
}

// bodyNames reads "setup" and "step" from a JSON body (either may be "").
func bodyNames(body []byte) (string, string) {
	var v struct {
		Setup string `json:"setup"`
		Step  string `json:"step"`
	}
	if len(body) == 0 || json.Unmarshal(body, &v) != nil {
		return "", ""
	}
	return v.Setup, v.Step
}

// errorOf reads code and message of a rest error envelope.
func errorOf(body []byte) (string, string) {
	var v struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &v) != nil {
		return "", strings.TrimSpace(excerpt(string(body)))
	}
	return v.Error.Code, v.Error.Message
}

// excerpt cuts s to debugExcerpt bytes.
func excerpt(s string) string {
	if len(s) <= debugExcerpt {
		return s
	}
	return strings.ToValidUTF8(s[:debugExcerpt], "") + "…"
}

// readCloser joins a reader with the original body's Close.
type readCloser struct {
	io.Reader
	io.Closer
}

// captureWriter remembers the status and the start of an error body.
type captureWriter struct {
	http.ResponseWriter
	status  int
	errBody bytes.Buffer
}

// WriteHeader records code and passes it on.
func (c *captureWriter) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

// Write keeps the first debugErrBody bytes of an error answer.
func (c *captureWriter) Write(b []byte) (int, error) {
	if c.status >= http.StatusBadRequest && c.errBody.Len() < debugErrBody {
		c.errBody.Write(b[:min(len(b), debugErrBody-c.errBody.Len())])
	}
	return c.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the real writer.
func (c *captureWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
