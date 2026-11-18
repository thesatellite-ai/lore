package orgcmd

import (
	"reflect"
	"strings"
	"testing"
)

// TestPushHelpNamesWhatSendsIt guards devco D-638: the --push help of the
// lore-org.yaml writes said "the next write to that store sends it", which
// is untrue under push manual (the default): the commit waits for
// `lore sync` or `lore store push`. The help names those two.
func TestPushHelpNamesWhatSendsIt(t *testing.T) {
	for _, in := range []any{StoreAddIn{}, StoreRemoveIn{}, ConfigSetIn{}, StoreAudienceIn{}} {
		f, ok := reflect.TypeOf(in).FieldByName("Push")
		if !ok {
			t.Fatalf("%T has no Push field", in)
		}
		desc := f.Tag.Get("desc")
		if strings.Contains(desc, "next write") {
			t.Errorf("%T --push help still says the next write sends it: %s", in, desc)
		}
		if !strings.Contains(desc, "lore sync") || !strings.Contains(desc, "lore store push --store <org>") {
			t.Errorf("%T --push help does not name lore sync and lore store push --store <org>: %s", in, desc)
		}
	}
}
