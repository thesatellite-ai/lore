package projectshared

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// TestRetiredRefuse pins Q-MODEB: both verbs refuse with E_UNSUPPORTED and
// name the stores commands, whatever they are given; the registry accepts
// the specs (Local, no confirm rule).
func TestRetiredRefuse(t *testing.T) {
	t.Parallel()
	reg, err := op.NewRegistry(Ops()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{OpSharedInit, OpSharedList} {
		e, ok := reg.Lookup(name)
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		for _, body := range []string{`{}`, `{"args":["x"],"db":"/tmp/a.db","name":"p"}`} {
			in, derr := e.Decode(op.JSONDecoder{Body: []byte(body)})
			if derr != nil {
				t.Fatal(derr)
			}
			_, err := e.Invoke(context.Background(), in)
			if !errs.Has(err, errs.Unsupported) || !strings.Contains(err.Error(), "removed in 0.1.18") {
				t.Fatalf("%s %s: %v, want E_UNSUPPORTED", name, body, err)
			}
			var le *errs.Error
			if !errors.As(err, &le) || !strings.Contains(le.Hint, "lore store init") {
				t.Fatalf("%s: hint %v does not name lore store init", name, err)
			}
		}
	}
}
