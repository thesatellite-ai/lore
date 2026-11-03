package dbent_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
)

// traceEnv is the switch the CLI documents as "print every git command lore
// runs for live rows". Database calls must print nothing under it.
const traceEnv = "LORE_TRACE"

// TestQueries_PrintNothingUnderTrace pins that a database query made through
// the client returned by dbent.New writes nothing to stdout or stderr, even
// with LORE_TRACE=1. A shipped binary once printed the absolute path of
// dbent/main.go on the build machine for every query under trace. Both
// streams are captured so a regression that moves the print to stderr still
// fails.
//
// Not parallel: it swaps the process-wide os.Stdout and os.Stderr.
func TestQueries_PrintNothingUnderTrace(t *testing.T) {
	t.Setenv(traceEnv, "1")

	client, db := openTestDB(t)
	defer client.Close()
	defer db.Close()

	out := captureOutput(t, func() {
		if _, err := client.Actor.Query().Count(context.Background()); err != nil {
			t.Fatalf("query: %v", err)
		}
	})

	if out != "" {
		t.Fatalf("a query under %s=1 printed %q; database calls must print nothing (and never a build-machine path)", traceEnv, out)
	}
}

// captureOutput runs fn with os.Stdout and os.Stderr redirected into one pipe
// and returns what fn wrote to either.
func captureOutput(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			buf.WriteString("capture error: " + err.Error())
		}
		done <- buf.String()
	}()
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	fn()
	os.Stdout, os.Stderr = oldOut, oldErr
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	return <-done
}
