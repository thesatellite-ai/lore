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
