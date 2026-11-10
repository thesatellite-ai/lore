package metaquery

import (
	"fmt"
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
		{"url:https://x.example", `text(url:https://x.example) | "url:https://x.example"`},
		{"title:invoice", `text(title:invoice) | "title:invoice"`},
		{"billing_rate?", "has(billing_rate) | "},
		{"what?", "text(what?) | what?"},
		{"jira:", "text(jira:) | \"jira:\""},
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
