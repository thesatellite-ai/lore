package period

import (
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

// TestOpsRegister: every period op's input and output types are ones the
// op layer accepts (op.Define panics at startup on an unsupported field
// kind, which once broke the whole binary), and the registry checks pass.
func TestOpsRegister(t *testing.T) {
	w := newWorld(t)
	if _, err := op.NewRegistry(Ops(w.f)...); err != nil {
		t.Fatal(err)
	}
}
