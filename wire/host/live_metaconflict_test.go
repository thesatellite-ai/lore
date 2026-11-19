package host

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/canonjson"
)

// TestLiveMetaConflicts (devco D-220): a strict meta entry holding
// {"conflict": {base, ours, theirs}} is listed by its key name with both
// values; a plain entry is not.
func TestLiveMetaConflicts(t *testing.T) {
	body, err := canonjson.Decode([]byte(`{"id":"tsk_1","meta":{"mky_a":{"name":"cost","strict":true,"v":{"conflict":{"base":null,"ours":4,"theirs":5}}},"mky_b":{"name":"client","v":"acme"}}}`), canonjson.DefaultMaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	got := liveMetaConflicts(body)
	if len(got) != 1 || got[0].name != "meta.cost" || got[0].key != "cost" || got[0].kept != "4" || got[0].other != "5" {
		t.Fatalf("meta conflicts = %+v", got)
	}
}
