package rest

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/op"
)

// slowRunner runs every operation after a pause longer than the write
// timeout the test sets: a request queued behind slow ones in `lore api`.
type slowRunner struct {
	*fake
	pause time.Duration
}

func (s slowRunner) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	time.Sleep(s.pause)
	return s.fake.Run(ctx, e, d, c)
}

// TestSlowOperationStillGetsItsReply: an operation that runs longer than
// the server's write timeout still answers (before the fix the server
// closed the connection and the client read EOF, while the operation ran).
// The operation is made longer than the deadline on purpose; a slower
// machine only makes it longer still, so the test cannot pass by luck.
// Not parallel: it shortens the package's writeTimeout.
func TestSlowOperationStillGetsItsReply(t *testing.T) {
	old := writeTimeout
	writeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { writeTimeout = old })
	f := newFake(t)
	s, err := New(f.reg, slowRunner{fake: f, pause: 6 * writeTimeout}, Options{Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln, io.Discard, "") }()
	t.Cleanup(func() { cancel(); <-done })
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/v1/memories", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("a slow operation lost its reply: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(body) == 0 {
		t.Fatalf("slow operation: %d %s", resp.StatusCode, body)
	}
}
