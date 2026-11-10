package wire

import (
	"bytes"
	"testing"
)

// TestSignalsTrace: LORE_TRACE=1 turns on one "trace: <line>" per signals
// fetch, push and retry on the given writer (LORE_TEAM_SPEC "pushes and
// retries"); unset or any other value gives no trace function, so nothing
// is printed and the store skips the formatting.
func TestSignalsTrace(t *testing.T) {
	for _, v := range []string{"", "0", "true", "yes", " 1"} {
		t.Run("off="+v, func(t *testing.T) {
			t.Setenv(envTrace, v)
			if signalsTrace(&bytes.Buffer{}) != nil {
				t.Fatalf("LORE_TRACE=%q must not trace", v)
			}
		})
	}
	t.Run("on", func(t *testing.T) {
		t.Setenv(envTrace, envTraceOn)
		var buf bytes.Buffer
		tr := signalsTrace(&buf)
		if tr == nil {
			t.Fatal("LORE_TRACE=1 must trace")
		}
		tr("signals: push 1 ref(s) atomic=false")
		tr("signals: lost race on refs/lore/signals (attempt 1), re-deciding")
		want := "trace: signals: push 1 ref(s) atomic=false\ntrace: signals: lost race on refs/lore/signals (attempt 1), re-deciding\n"
		if buf.String() != want {
			t.Fatalf("trace output %q, want %q", buf.String(), want)
		}
	})
}
