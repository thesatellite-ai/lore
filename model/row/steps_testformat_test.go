//go:build loretest_format

package row

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
)

// TestTestStepAdditive: only a row with the test key is written at _v 3.
func TestTestStepAdditive(t *testing.T) {
	r := Row{Table: "memories"}
	if writeVersion(r) != hlcVersion {
		t.Fatal("plain row left the release version")
	}
	r.Reserved = map[string]value.Value{testKey: value.BoolOf(true)}
	if writeVersion(r) != FormatVersion {
		t.Fatal("test row not written at the test version")
	}
}
