package probe

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestStartJobOwnTrace: a job's calls go to the sink as one record, not
// into the request's trace (already read when the request answered); a
// secret the request registered is redacted in the job's calls and error.
func TestStartJobOwnTrace(t *testing.T) {
	secret := "job-secret-" + strings.Repeat("q", 8)
	var got []JobRecord
	ctx, reqTrace := WithTrace(context.Background())
	ctx = WithJobSink(ctx, func(r JobRecord) { got = append(got, r) })
	Secret(ctx, secret)
	jctx, finish := StartJob(ctx, "discovery job")
	Record(WithLabel(jctx, "github.api"), Call{Command: "GET https://api.github.com/user?t=" + secret, Exit: 200})
	finish(errors.New("refused " + secret))
	if n := len(reqTrace.Calls()); n != 0 {
		t.Fatalf("request trace got %d job calls", n)
	}
	if len(got) != 1 || got[0].Name != "discovery job" || len(got[0].Calls) != 1 || got[0].Calls[0].Label != "github.api" {
		t.Fatalf("records %+v", got)
	}
	if strings.Contains(got[0].Calls[0].Command, secret) || strings.Contains(got[0].Err, secret) || !strings.Contains(got[0].Err, "refused") {
		t.Fatalf("not redacted: %+v", got[0])
	}
}

// TestStartJobNoSink: without a sink the job records nothing and leaves
// the request's trace alone; finish is safe to call.
func TestStartJobNoSink(t *testing.T) {
	ctx, reqTrace := WithTrace(context.Background())
	jctx, finish := StartJob(ctx, "discovery job")
	Record(jctx, Call{Command: "GET https://example.invalid"})
	finish(nil)
	if TraceFrom(jctx) != nil || len(reqTrace.Calls()) != 0 {
		t.Fatalf("job wrote into the request trace: %d", len(reqTrace.Calls()))
	}
}
