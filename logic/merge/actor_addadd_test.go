package merge

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
)

// TestSamePersonOnTwoMachinesAddAddIsClean (F18 / N1): two machines of
// one person create the same actor row (same act_ id from the stable key)
// before either has the other's: an add/add with identical fields and
// different stamps. The core merge must be clean: identical fields stay,
// the newer HLC and updated_at win, created_at is the EARLIER side's, and
// the result is independent of which side is ours. A real difference in a
// text field (display_name) stays a loud clash, never a silent pick. These are the exact bytes the CLI wrote in the e2e run
// (TestF18_SamePersonOnSeveralMachinesKeepsOneRepoActor).
func TestSamePersonOnTwoMachinesAddAddIsClean(t *testing.T) {
	doc := func(stamp, display string) []byte {
		return []byte(`{"_hlc":"` + stamp + `Z/0000/act_02638083ff857bcb918d37651f695705","_o":{"cmd":"rule add","store":"repo"},` +
			`"_table":"actors","_v":2,"created_at":"` + stamp + `000000Z","created_by":"act_02638083ff857bcb918d37651f695705",` +
			`"display_name":"` + display + `","id":"act_02638083ff857bcb918d37651f695705","kind":"human",` +
			`"updated_at":"` + stamp + `000000Z","updated_by":"act_02638083ff857bcb918d37651f695705"}`)
	}
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	d := Driver{Registry: reg}
	early, late := "2026-11-04T13:24:35.494", "2026-11-04T13:24:35.731"
	cases := []struct {
		name                  string
		ours, theirs          []byte
		wantDisplay, wantHLCs string
	}{
		{"identical fields, ours newer", doc(late, "Alice Doe"), doc(early, "Alice Doe"), "Alice Doe", late},
		{"identical fields, theirs newer", doc(early, "Alice Doe"), doc(late, "Alice Doe"), "Alice Doe", late},
		{"unicode name, same both sides", doc(late, "Ålice Dö"), doc(early, "Ålice Dö"), "Ålice Dö", late},
	}
	if _, conflict, err := d.Merge(nil, doc(early, "Alice"), doc(late, "Alice Doe")); err != nil || !conflict {
		t.Fatalf("two different display names must stay a visible clash: %v %v", conflict, err)
	}
	var first []byte
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, conflict, err := d.Merge(nil, c.ours, c.theirs)
			if err != nil || conflict || bytes.Contains(out, []byte("<<<<<<<")) {
				t.Fatalf("add/add of one actor must merge clean: conflict %v err %v\n%s", conflict, err, out)
			}
			var m map[string]any
			if err = json.Unmarshal(out, &m); err != nil {
				t.Fatal(err)
			}
			if m["display_name"] != c.wantDisplay || m["created_at"] != early+"000000Z" || m["updated_at"] != c.wantHLCs+"000000Z" {
				t.Fatalf("merged %v", m)
			}
			if i == 0 {
				first = out
			}
			if i == 1 && !bytes.Equal(out, first) {
				t.Fatalf("the merge depends on which side is ours:\n%s\n%s", first, out)
			}
		})
	}
}
