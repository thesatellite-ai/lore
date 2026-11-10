package tastepref_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/schema"
)

// spec is v0.1.17's tastepref facts (read from its real output, 2026-11-04).
var spec = contenttest.Spec{
	Noun: "tastepref", Table: schema.TableTastePrefs, Prefix: "tst",
	KindList: "tastePref.list", KindShow: "tastePref.show", KindSearch: "tastepref.search",
	Empty: "(no tasteprefs)\n", Badge: "TA", TitleKey: "",
	ListLine: contenttest.TitleLine("TA", "body"),
	Add: func(title, body string) string {
		return `{"body":` + contenttest.Quote(body) + `}`
	},
	RecordKeys: []string{"id", "created_at", "updated_at", "project_id", "body", "created_by_actor_id"},
	HitKeys:    []string{"body", "scope"},
	EditWord:   "tastepref",
	EditTitle: func(id, v string) string {
		return `{"id":"` + id + `","body":` + contenttest.Quote(v) + `}`
	},
	SearchLine: func(id, _, body string) string { return id + " " + cutRunes(body, 80) + "\n" },
}

func TestTasteprefConformance(t *testing.T) {
	contenttest.Check(t, spec, func(t *testing.T) contenttest.Runner { return opstest.New(t) })
}

// cutRunes is v0.1.17's trunc(body, 80) on characters: n-3 characters
// plus "..." when longer than n.
func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

// TestScopeAndCut: edit --scope sets the free-form scope (search
// --column scope finds it); the search line cuts a long body at 80
// characters on a character boundary (v0.1.17 trunc), so a unicode body
// never prints half a character.
func TestScopeAndCut(t *testing.T) {
	h := opstest.New(t)
	long := "retry " + strings.Repeat("é😀", 60)
	id := h.ID(h.Must("tastepref.add", `{"body":`+opstest.Quote(long)+`}`))
	h.Must("tastepref.edit", `{"id":"`+id+`","scope":"frontend"}`)
	if got := contenttest.Str(t, h.JSON(h.Must("tastepref.show", `{"id":"`+id+`"}`)), "data", "scope"); got != "frontend" {
		t.Errorf("scope %q", got)
	}
	if !strings.Contains(h.JSON(h.Must("tastepref.search", `{"query":"frontend","columns":["scope"]}`)), id) {
		t.Error("search --column scope misses the row")
	}
	line := opstest.Text(h.Must("tastepref.search", `{"query":"retry"}`))
	want := id + " " + string([]rune(long)[:77]) + "...\n"
	if line != want {
		t.Errorf("search line\n%q\nwant\n%q", line, want)
	}
	if !utf8.ValidString(line) {
		t.Error("search line is not valid UTF-8")
	}
	short := h.ID(h.Must("tastepref.add", `{"body":"exactly short"}`))
	if got := opstest.Text(h.Must("tastepref.search", `{"query":"exactly"}`)); got != short+" exactly short\n" {
		t.Errorf("short body line %q", got)
	}
}
