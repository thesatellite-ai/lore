package wire_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/schema"
)

// searchMoreAdds is the add input of each noun with a `<noun> search`,
// before the closing brace (%N% numbers a name that must be unique, %ON%
// is a row to comment on); every
// one carries the word "quokka". A new
// search op fails TestEverySearchSaysWhenCut until it is listed here.
var searchMoreAdds = map[string]string{
	"architecturenote": `{"title":"quokka layout","body":"quokka note"`,
	"behaviour":        `{"name":"quokka-habit-%N%","body":"quokka habit"`,
	"comment":          `{"on_id":"%ON%","body":"quokka remark"`,
	"cookbookrecipe":   `{"title":"quokka recipe","body":"quokka steps"`,
	"decision":         `{"title":"quokka call","body":"quokka decided"`,
	"handoff":          `{"for":"@quinn","body":"quokka state"`,
	"hotfix":           `{"title":"quokka fix","body":"quokka patch"`,
	"incident":         `{"title":"quokka outage","body":"quokka down"`,
	"memory":           `{"body":"quokka fact"`,
	"mission":          `{"title":"quokka mission","body":"quokka goal"`,
	"pattern":          `{"title":"quokka pattern","body":"quokka shape"`,
	"plan":             `{"title":"quokka plan","body":"quokka steps"`,
	"playbook":         `{"title":"quokka playbook","body":"quokka run"`,
	"prompt":           `{"title":"quokka prompt %N%","body":"quokka ask"`,
	"rule":             `{"body":"quokka rule"`,
	"snapshot":         `{"title":"quokka week","body":"quokka known"`,
	"suggestion":       `{"title":"quokka idea","body":"quokka maybe"`,
	"task":             `{"title":"quokka task","body":"quokka work"`,
	"tasklist":         `{"title":"quokka list","body":"quokka items"`,
	"tastepref":        `{"body":"quokka likes"`,
	"techdoc":          `{"name":"quokka-doc-%N%","description":"quokka spec"`,
	"workflow":         `{"name":"quokka-flow-%N%","body":"quokka stages"`,
	"workspace":        `{"name":"quokka-space-%N%","body":"quokka place"`,
}

// TestEverySearchSaysWhenCut (devco D-770, follow-up of D-767) walks every
// registered `<noun>.search`: three rows match; --limit 2 lists two, ends
// with the morehits note and --json says "more": true; --limit 3 lists all
// three and says nothing more. Each search cut at --limit with no word, so
// a count or list from one call was wrong and nothing said so.
func TestEverySearchSaysWhenCut(t *testing.T) {
	h := opstest.New(t)
	on := h.ID(h.MustYes("memory.add", `{"body":"a row to comment on"}`))
	h.Seed(schema.TableActors, map[string]string{"kind": `"human"`, "display_name": `"Quinn"`, "handle": `"quinn"`, "stable_key": `"human:quinn@example.com"`}) // a handoff is for someone
	walked := 0
	for _, e := range h.App.Registry().Entries() {
		noun, isSearch := strings.CutSuffix(e.Spec().Name, ".search")
		if !isSearch || noun == "search" {
			continue // search.run has its own test (TestGlobalSearchSaysWhenCut)
		}
		walked++
		add, ok := searchMoreAdds[noun]
		if !ok {
			t.Errorf("%s search: no add input in searchMoreAdds; add one so its cut is checked", noun)
			continue
		}
		add = strings.ReplaceAll(add, "%ON%", on)
		for i := range 3 {
			h.MustYes(noun+".add", strings.ReplaceAll(add, "%N%", strconv.Itoa(i))+"}")
		}
		for _, c := range []struct {
			limit string
			hits  int
			more  bool
		}{{"2", 2, true}, {"3", 3, false}} {
			in := `{"query":"quokka","limit":` + c.limit + `}`
			if noun == "memory" {
				in = `{"query":"quokka fact","limit":` + c.limit + `}`
			}
			res := h.Must(noun+".search", in)
			var out struct {
				Data []json.RawMessage `json:"data"`
				More bool              `json:"more"`
			}
			if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
				t.Fatalf("%s search: %v", noun, err)
			}
			txt := opstest.Text(res)
			said := strings.Contains(txt, "note: more rows match than the "+c.limit+" listed")
			if len(out.Data) != c.hits || out.More != c.more || said != c.more {
				t.Errorf("%s search --limit %s: %d hits more=%v note=%v, want %d more=%v; text:\n%s", noun, c.limit, len(out.Data), out.More, said, c.hits, c.more, txt)
			}
		}
	}
	if walked < len(searchMoreAdds) {
		t.Errorf("walked %d search ops, searchMoreAdds lists %d: drop the gone ones", walked, len(searchMoreAdds))
	}
}
