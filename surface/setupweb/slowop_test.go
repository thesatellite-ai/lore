package setupweb

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/op"
)

// slowRunner runs every operation after a pause longer than the write
// timeout the test sets: a doctor run probing ssh, a setup apply cloning.
type slowRunner struct {
	*fakeRunner
	pause time.Duration
}

func (s slowRunner) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	time.Sleep(s.pause)
	return s.fakeRunner.Run(ctx, e, d, c)
}

// TestSlowSetupOperationStillGetsItsReply: a setup page operation that runs
// longer than the server's write timeout still answers, through the real
// server and every handler wrapper. It holds only while the /v1 handler
// lifts the deadline for the run (surface/rest runUnbounded) and every
// wrapper of the page unwraps to the connection (statusWriter,
// captureWriter); either missing, the page gets a dropped connection (EOF).
// The operation is made longer than the deadline on purpose; a slower
// machine only makes it longer, so it cannot pass by luck.
// Not parallel: it shortens the package's writeTimeout.
func TestSlowSetupOperationStillGetsItsReply(t *testing.T) {
	old := writeTimeout
	writeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { writeTimeout = old })
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(ln, Options{Registry: fakeRegistry(t), Runner: slowRunner{fakeRunner: &fakeRunner{calls: map[string]int{}}, pause: 6 * writeTimeout}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- p.Serve(ctx, ln, io.Discard, nil) }()
	t.Cleanup(func() { cancel(); <-done })
	base := "http://" + p.host
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	cl := &http.Client{Jar: jar}
	post := func(path, body, csrf string) (int, string, error) {
		q, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
		if err != nil {
			return 0, "", err
		}
		q.Header.Set("Content-Type", "application/json")
		q.Header.Set("Origin", base)
		if csrf != "" {
			q.Header.Set(HeaderCSRF, csrf)
		}
		resp, err := cl.Do(q)
		if err != nil {
			return 0, "", err
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, string(b), nil
	}
	code, body, err := post(PathSession, `{"token":"`+p.token+`"}`, "")
	if err != nil || code != http.StatusOK {
		t.Fatalf("session: %d %s %v", code, body, err)
	}
	csrf := body[strings.Index(body, `"csrf":"`)+len(`"csrf":"`):]
	csrf = csrf[:strings.IndexByte(csrf, '"')]
	code, body, err = post("/v1/setup/plan", `{"kind":"solo"}`, csrf)
	if err != nil {
		t.Fatalf("a slow setup operation lost its reply: %v", err)
	}
	if code != http.StatusOK || !strings.Contains(body, "solo") {
		t.Fatalf("slow setup operation: %d %s", code, body)
	}
}
