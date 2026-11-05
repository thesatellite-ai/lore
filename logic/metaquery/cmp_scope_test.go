package metaquery

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/meta"
)

// TestParseComparisons pins OV39-CMP / P2-META-2: >, >=, <, <=, !=, ~= on a
// visible key become Cmp terms; << and !? stay text, a name that is not a
// key stays text, and the value keeps any operator characters after the
// first one.
func TestParseComparisons(t *testing.T) {
	keys := map[string]bool{"impact": true, "a": true, "net": true, "due": true, "deploy": true}
	isKey := func(n string) bool { return keys[n] }
	cases := []struct {
		q    string
		kind TermKind
		op   meta.Op
		name string
		val  string
	}{
		{"impact>3", Cmp, meta.OpGt, "impact", "3"},
		{"a>=1", Cmp, meta.OpGe, "a", "1"},
		{"a<=1", Cmp, meta.OpLe, "a", "1"},
		{"a<1", Cmp, meta.OpLt, "a", "1"},
		{"a!=x", Cmp, meta.OpNe, "a", "x"},
		{"a~=X", Cmp, meta.OpLike, "a", "X"},
		{"due>=2026-01-01", Cmp, meta.OpGe, "due", "2026-01-01"},
		{"deploy.region!=eu", Cmp, meta.OpNe, "deploy.region", "eu"},
		{`a~="Two Words"`, Cmp, meta.OpLike, "a", "Two Words"},
		{"a>=<1", Cmp, meta.OpGe, "a", "<1"}, // first operator wins, rest is value
		{"a=b=c", Cond, "", "a", "b=c"},
		{"a:>3", Cond, "", "a", ">3"}, // ':' first: exact match on ">3"
		{"net<<10.0.0.0/8", Text, "", "", ""},
		{"a!?", Text, "", "", ""},
		{"unknown>3", Text, "", "", ""},
		{"Impact>3", Text, "", "", ""}, // key names are case-sensitive
		{">3", Text, "", "", ""},
		{"a>", Text, "", "", ""},
		{"a!=", Text, "", "", ""},
	}
	for _, c := range cases {
		ts := Parse(c.q, isKey)
		if len(ts) != 1 {
			t.Fatalf("%q: %d terms", c.q, len(ts))
		}
		got := ts[0]
		if got.Kind != c.kind {
			t.Errorf("%q: kind %d, want %d", c.q, got.Kind, c.kind)
			continue
		}
		if got.Word != c.q {
			t.Errorf("%q: word %q", c.q, got.Word)
		}
		if c.kind == Text {
			continue
		}
		if got.Op != c.op || got.Name() != c.name || got.Value != c.val {
			t.Errorf("%q: op %q name %q value %q, want %q %q %q", c.q, got.Op, got.Name(), got.Value, c.op, c.name, c.val)
		}
	}
	if fts := FTS(Parse("impact>3 net<<10.0.0.0/8 booking", isKey)); fts != `"net<<10.0.0.0/8" booking` {
		t.Errorf("FTS = %q", fts)
	}
}

// TestParseScopes pins P2-META-9: level:term and level:place:term (repo,
// project; org takes no place); a level word not followed by a key term is
// a plain term.
func TestParseScopes(t *testing.T) {
	keys := map[string]bool{"jira": true, "impact": true, "client": true}
	isKey := func(n string) bool { return keys[n] }
	cases := []struct {
		q            string
		kind         TermKind
		level, place string
		name, val    string
	}{
		{"repo:jira:HUB-1", Cond, LevelRepo, "", "jira", "HUB-1"},
		{"repo:acmehub_backend:jira:HUB-1", Cond, LevelRepo, "acmehub_backend", "jira", "HUB-1"},
		{"project:acme-hub:client?", Has, LevelProject, "acme-hub", "client", ""},
		{"project:client?", Has, LevelProject, "", "client", ""},
		{"org:impact>3", Cmp, LevelOrg, "", "impact", "3"},
		{"org:agency:impact>3", Text, "", "", "", ""}, // org takes no place
		{"repo:hello", Text, "", "", "", ""},          // level word before a non-key
		{"repo:x:hello", Text, "", "", "", ""},
		{"repo:", Text, "", "", "", ""},
		{"repo::jira:1", Text, "", "", "", ""}, // empty place
		{"team:jira:1", Text, "", "", "", ""},  // not a level word
		{"jira:repo:x", Cond, "", "", "jira", "repo:x"},
		{"REPO:jira:1", Text, "", "", "", ""},
	}
	for _, c := range cases {
		ts := Parse(c.q, isKey)
		if len(ts) != 1 {
			t.Fatalf("%q: %d terms", c.q, len(ts))
		}
		got := ts[0]
		if got.Kind != c.kind {
			t.Errorf("%q: kind %d, want %d", c.q, got.Kind, c.kind)
			continue
		}
		if c.kind == Text {
			if got.Level != "" || got.Place != "" {
				t.Errorf("%q: text term has scope %q/%q", c.q, got.Level, got.Place)
			}
			continue
		}
		if got.Level != c.level || got.Place != c.place || got.Name() != c.name || got.Value != c.val || got.Word != c.q {
			t.Errorf("%q: level %q place %q name %q value %q word %q", c.q, got.Level, got.Place, got.Name(), got.Value, got.Word)
		}
	}
}
