package row

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOriginFolderLine: `lore show --origin` prints the store folder of a
// row written into a store folder (`_o.folder`, its prj_ id, with the
// folder's name when known) right under the store line, and --json
// carries origin.folder; a row with no folder prints no folder line and
// no key.
func TestOriginFolderLine(t *testing.T) {
	const sto, fld = "sto_01a1a5aca7f57659be7320c5f4b70011", "prj_01a1a5aca7f57659be7320c5f4b70022"
	cases := []struct {
		name   string
		folder Named
		line   string
	}{
		{"named folder", Named{ID: fld, Name: "acme"}, "folder     acme  " + fld},
		{"id only", Named{ID: fld}, "folder     " + fld},
		{"no folder", Named{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := Origin{Origin: OriginBlock{Recorded: true, Cmd: "memory add", Store: sto, Folder: c.folder}}
			text := g.text()
			if c.line == "" {
				if strings.Contains(text, "folder ") {
					t.Fatalf("a folder line without a folder:\n%s", text)
				}
			} else if !strings.Contains(text, "  store      "+sto+"\n  "+c.line+"\n") {
				t.Fatalf("want the folder line under the store line %q:\n%s", c.line, text)
			}
			b, err := json.Marshal(g.Origin)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), `"folder"`) != (c.folder.ID != "") {
				t.Fatalf("json %s", b)
			}
		})
	}
}
