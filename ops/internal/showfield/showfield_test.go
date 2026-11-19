package showfield

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
)

// TestListStaysAList (D-455): a list value is a JSON array, text joined;
// any other value a JSON string; the map reads its own JSON back.
func TestListStaysAList(t *testing.T) {
	l, err := value.ParseList(value.KindText, []byte(`["act_a","act_b"]`))
	if err != nil {
		t.Fatal(err)
	}
	title, err := value.Parse(value.KindText, []byte(`"ship it"`))
	if err != nil {
		t.Fatal(err)
	}
	m := Map{"watchers": Of(l), "title": Of(title)}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"title":"ship it","watchers":["act_a","act_b"]}`; got != want {
		t.Fatalf("json %s, want %s", got, want)
	}
	if m["watchers"].String() != "act_a, act_b" {
		t.Errorf("text %q", m["watchers"].String())
	}
	var back Map
	if err := json.Unmarshal(b, &back); err != nil || !back["watchers"].IsList || back["title"].String() != "ship it" {
		t.Fatalf("back %+v %v", back, err)
	}
}
