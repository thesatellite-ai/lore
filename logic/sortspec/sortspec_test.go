package sortspec

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

func TestParse(t *testing.T) {
	ok := map[string]Spec{
		"name":          {Key: "name"},
		"name:asc":      {Key: "name"},
		"name:desc":     {Key: "name", Desc: true},
		"due_at:desc":   {Key: "due_at", Desc: true},
		"meta.cost:asc": {Key: "meta.cost"},
		"k9":            {Key: "k9"},
	}
	for in, want := range ok {
		got, err := Parse([]string{in})
		if err != nil || len(got) != 1 || got[0] != want {
			t.Errorf("%q: %+v %v", in, got, err)
		}
	}
	bad := map[string]string{
		"-name":      "use name:desc",
		"-":          "use :desc",
		"":           "no key",
		":desc":      "no key",
		"name:down":  "use name:asc or name:desc",
		"name:DESC":  "the direction is asc or desc",
		"name:":      "the direction is asc or desc",
		"Name":       "lowercase ASCII",
		"nämé":       "lowercase ASCII",
		"9x":         "lowercase ASCII",
		"a b":        "lowercase ASCII",
		"name:desc:": "the direction is asc or desc",
	}
	for in, want := range bad {
		_, err := Parse([]string{in})
		if err == nil || !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%q: %v, want E_INVALID_INPUT", in, err)
			continue
		}
		msg := err.Error()
		var e *errs.Error
		if errors.As(err, &e) {
			msg += " " + e.Hint
		}
		if !strings.Contains(msg, want) {
			t.Errorf("%q: %q lacks %q", in, msg, want)
		}
	}
	specs, err := Parse([]string{"a", "b:desc"})
	if err != nil || !slices.Equal(specs, []Spec{{Key: "a"}, {Key: "b", Desc: true}}) {
		t.Fatalf("repeated: %v %v", specs, err)
	}
	if specs[1].String() != "b:desc" || specs[0].String() != "a:asc" {
		t.Fatal("String")
	}
}

func TestCheck(t *testing.T) {
	specs, _ := Parse([]string{"name", "size:desc"})
	if err := Check(specs, []string{"name", "size"}); err != nil {
		t.Fatal(err)
	}
	err := Check(specs, []string{"name"})
	if err == nil || !strings.Contains(err.Error(), "unknown key size (keys: name)") {
		t.Fatalf("got %v", err)
	}
}

// TestCompare is the value kind × direction × missing matrix.
func TestCompare(t *testing.T) {
	type rec map[string]Value
	t0 := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	rows := []rec{
		{"n": Text("b"), "p": Rank(2), "d": Time(t0.Add(time.Hour)), "c": Number(10)},
		{"n": Text("a"), "p": Rank(3), "d": Missing, "c": Number(2)},
		{"n": Text("c"), "p": Rank(2), "d": Time(t0), "c": Number(2.5)},
		{"n": Text(""), "p": Rank(0), "d": Time(time.Time{}), "c": Missing},
	}
	order := func(raw ...string) []int {
		specs, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		idx := []int{0, 1, 2, 3}
		slices.SortStableFunc(idx, func(i, j int) int {
			return Compare(specs, func(k string) Value { return rows[i][k] }, func(k string) Value { return rows[j][k] })
		})
		return idx
	}
	cases := []struct {
		sort []string
		want []int
	}{
		{[]string{"n"}, []int{1, 0, 2, 3}},           // "" is missing → last
		{[]string{"n:desc"}, []int{2, 0, 1, 3}},      // missing still last
		{[]string{"c"}, []int{1, 2, 0, 3}},           // numeric: 2 < 2.5 < 10
		{[]string{"c:desc"}, []int{0, 2, 1, 3}},      // not text order
		{[]string{"d"}, []int{2, 0, 1, 3}},           // zero time and missing last, stable
		{[]string{"d:desc"}, []int{0, 2, 1, 3}},      //
		{[]string{"p:desc", "n"}, []int{1, 0, 2, 3}}, // tie on rank 2 broken by name
		{[]string{"p:desc", "n:desc"}, []int{1, 2, 0, 3}},
		{nil, []int{0, 1, 2, 3}}, // no spec keeps the caller's order
	}
	for _, c := range cases {
		if got := order(c.sort...); !slices.Equal(got, c.want) {
			t.Errorf("%v: got %v want %v", c.sort, got, c.want)
		}
	}
}
