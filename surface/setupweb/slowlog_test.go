package setupweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSlowSetupCallIsLogged: a /v1 operation call slower than SlowRequest
// prints one terminal line naming method, path, status and duration, so
// a setup page that feels slow shows which call was slow. Fast calls and
// non-/v1 paths (the page's own files) print nothing.
func TestSlowSetupCallIsLogged(t *testing.T) {
	var log strings.Builder
	var took time.Duration
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	calls := 0
	p := &Page{slowLog: &log, opts: Options{Now: func() time.Time {
		calls++
		if calls%2 == 0 {
			return now.Add(took) // the end of the request
		}
		return now
	}}}
	h := p.timed(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	serve := func(path string, d time.Duration) {
		took = d
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, nil))
	}
	serve("/v1/doctor.run", SlowRequest+250*time.Millisecond)
	want := "setup page: POST /v1/doctor.run 202 in 750ms\n"
	if log.String() != want {
		t.Fatalf("slow call log = %q, want %q", log.String(), want)
	}
	log.Reset()
	serve("/v1/doctor.run", SlowRequest-time.Millisecond)
	serve("/app.js", 3*SlowRequest)
	if log.Len() != 0 {
		t.Fatalf("a fast call or a non-/v1 path was logged: %q", log.String())
	}
}
