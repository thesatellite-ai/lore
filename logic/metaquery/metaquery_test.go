package metaquery

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestSearchParse(t *testing.T) {
	keys := map[string]bool{"jira": true, "deploy": true, "note": true, "billing_rate": true}
	isKey := func(n string) bool { return keys[n] }
	show := func(ts []Term) string {
		var parts []string
		for _, x := range ts {
			switch x.Kind {
			case Cond:
				parts = append(parts, fmt.Sprintf("cond(%s=%s)", x.Name(), x.Value))
			case Has:
				parts = append(parts, fmt.Sprintf("has(%s)", x.Name()))
			case Cmp:
				parts = append(parts, fmt.Sprintf("cmp(%s%s%s)", x.Name(), x.Op, x.Value))
			case Text:
				parts = append(parts, "text("+x.Word+")")
			}
		}
		return strings.Join(parts, " ") + " | " + FTS(ts)
	}
	cases := []struct{ q, want string }{
		{"HUB-412", `text(HUB-412) | "HUB-412"`},
		{"HUB-41*", `text(HUB-41*) | "HUB-41"*`},
		{"HUB-412 booking", `text(HUB-412) text(booking) | "HUB-412" booking`},
		{"jira:HUB-412", "cond(jira=HUB-412) | "},
		{"jira=HUB-412 404", "cond(jira=HUB-412) text(404) | 404"},
		{"3.5", `text(3.5) | "3.5"`},
		{"deploy.region:eu-central", "cond(deploy.region=eu-central) | "},
		{"note:10:30", "cond(note=10:30) | "},
		{`note:"10:30 standup"`, "cond(note=10:30 standup) | "},
		{"url:https://x.example", `text(url:https://x.example) | url "https://x.example"`},
		{"title:invoice", `text(title:invoice) | title invoice`},
		{"billing_rate?", "has(billing_rate) | "},
		{"what?", "text(what?) | what?"},
		{"jira:", "text(jira:) | jira"},
		{`"exact phrase" a OR b`, `text("exact phrase") text(a) text(OR) text(b) | "exact phrase" a OR b`},
		{"", " | "},
		{`say "x"-y`, `text(say) text("x"-y) | say "x"-y`},
		{`a-"b`, `text(a-"b) | "a-""b"`},
	}
	for _, c := range cases {
		if got := show(Parse(c.q, isKey)); got != c.want {
			t.Errorf("%q:\n got %s\nwant %s", c.q, got, c.want)
		}
	}
}

// TestFTSNegated: the FTS5 text of a query with '-' terms (one negation
// per term starting with '-', wherever it stands; a '-' inside a word or a
// lone '-' is text) and Excluded / OnlyNegated for the cases FTS5's NOT
// cannot express (no word to subtract from).
func TestFTSNegated(t *testing.T) {
	none := func(string) bool { return false }
	cases := []struct {
		q, fts, excluded string
		only             bool
	}{
		{"deploy", "deploy", "", false},
		{"deploy -friday", "(deploy) NOT friday", "friday", false},
		{"-friday deploy", "(deploy) NOT friday", "friday", false},
		{"a b -c -d", "(a b) NOT c NOT d", "c OR d", false},
		{"deploy -fri*", "(deploy) NOT fri*", "fri*", false},
		{`deploy -"on call"`, `(deploy) NOT "on call"`, `"on call"`, false},
		{"deploy -x-ray", `(deploy) NOT "x-ray"`, `"x-ray"`, false},
		{"well-known", `"well-known"`, "", false},
		{"deploy -", `deploy "-"`, "", false},
		{"deploy --x", `deploy "--x"`, "", false},
		{"-friday", "", "friday", true},
		{"-friday -monday", "", "friday OR monday", true},
		{"", "", "", false},
	}
	for _, c := range cases {
		terms := Parse(c.q, none)
		if got := FTS(terms); got != c.fts {
			t.Errorf("FTS(%q) = %q, want %q", c.q, got, c.fts)
		}
		if got := Excluded(terms); got != c.excluded {
			t.Errorf("Excluded(%q) = %q, want %q", c.q, got, c.excluded)
		}
		if got := OnlyNegated(c.q); got != c.only {
			t.Errorf("OnlyNegated(%q) = %v, want %v", c.q, got, c.only)
		}
	}
}

// TestColonParts (devco D-964, H-223): a ':' inside a text word separates
// words, so lint:no-os-getenv finds a row holding "the no-os-getenv lint";
// before, the word went to FTS5 as one phrase (adjacent words) and found
// nothing. Times and quoted words stay whole; key:value on a known key
// stays a condition.
func TestColonParts(t *testing.T) {
	cases := []struct {
		w    string
		want []string
	}{
		{"lint:no-os-getenv", []string{"lint", "no-os-getenv"}},
		{"gqlkit:all", []string{"gqlkit", "all"}},
		{"a:b:c", []string{"a", "b", "c"}},
		{"lint:no*", []string{"lint", "no*"}},
		{":lead", []string{"lead"}},
		{"trail:", []string{"trail"}},
		{"a::b", []string{"a", "b"}},
		{":", nil},
		{`foo:"bar baz"`, []string{"foo", `"bar baz"`}},
		{`"lint:x"`, []string{`"lint:x"`}},
		{"10:30", []string{"10:30"}},
		{"16:9", []string{"16:9"}},
		{"plain", []string{"plain"}},
		{"a.b/c-d", []string{"a.b/c-d"}},
		{"https://x.example/a", []string{"https://x.example/a"}},
		{"see:https://x.example", []string{"see", "https://x.example"}},
	}
	for _, c := range cases {
		if got := ColonParts(c.w); !slices.Equal(got, c.want) {
			t.Errorf("ColonParts(%q) = %q, want %q", c.w, got, c.want)
		}
	}
	none := func(string) bool { return false }
	for q, want := range map[string]string{
		"lint:runq-determinism exempt": `lint "runq-determinism" exempt`,
		"gqlkit:all":                   "gqlkit all",
		"lint:no* deploy":              "lint no* deploy",
		"at 10:30":                     `at "10:30"`,
		`"lint:x"`:                     `"lint:x"`,
		"deploy -lint:x":               `(deploy) NOT "lint:x"`,
	} {
		if got := FTS(Parse(q, none)); got != want {
			t.Errorf("FTS(%q) = %q, want %q", q, got, want)
		}
	}
	// a known key still reads as a condition, not as two words
	key := func(n string) bool { return n == "status" }
	if got := Parse("status:open lint:x", key); len(got) != 2 || got[0].Kind != Cond || got[1].Kind != Text {
		t.Errorf("Parse with key = %+v, want a Cond and a Text", got)
	}
}
