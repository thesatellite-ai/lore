package settings

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

func TestInferType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		vals []string
		want string
		note string
	}{
		{[]string{"1", "2.5"}, "number", ""},
		{[]string{"true", "false"}, "bool", ""},
		{[]string{`["a"]`}, "list", ""},
		{[]string{`{"a":1}`}, "json", ""},
		{[]string{`"2026-11-05"`}, "text", "not typed as dates"},
		{[]string{`"2026-11-05T10:00:00Z"`}, "text", "not typed as dates"},
		{[]string{`"x"`, "1"}, "text", "mixed"},
		{nil, "text", "no values"},
	}
	for _, c := range cases {
		m := missingKey{name: "k"}
		for _, v := range c.vals {
			m.vals = append(m.vals, []byte(v))
		}
		got, note := inferType(m)
		if got != c.want || !strings.Contains(note, c.note) {
			t.Errorf("%v: got %q (%q), want %q (%q)", c.vals, got, note, c.want, c.note)
		}
	}
}

func TestRowSeesPlace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		row, place store.Source
		want       bool
	}{
		{store.SourceRepo, store.SourceRepo, true},
		{"agency/hub", store.SourceRepo, false},
		{store.SourceRepo, "agency/hub", true},
		{"agency/web", "agency/hub", true},
		{"other/hub", "agency/hub", false},
	}
	for _, c := range cases {
		if got := rowSeesPlace(c.row, c.place); got != c.want {
			t.Errorf("rowSeesPlace(%s, %s) = %v", c.row, c.place, got)
		}
	}
}
