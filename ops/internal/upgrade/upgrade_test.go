package upgrade

import "testing"

type clock interface{ Now() int }

type withClock struct{}

func (withClock) Now() int { return 7 }

type plain struct{}

// TestAs pins both answers of the upgrade check: an Env that implements the
// capability yields it typed; one that doesn't yields ok=false and the zero
// value (never a panic), including a nil interface.
func TestAs(t *testing.T) {
	t.Parallel()
	if c, ok := As[clock](withClock{}); !ok || c.Now() != 7 {
		t.Fatalf("withClock: ok=%v", ok)
	}
	if c, ok := As[clock](plain{}); ok || c != nil {
		t.Fatalf("plain: ok=%v c=%v", ok, c)
	}
	var none clock
	if _, ok := As[clock](none); ok {
		t.Fatal("nil interface must not upgrade")
	}
}
