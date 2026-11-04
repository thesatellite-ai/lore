package filter

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestParseTerms pins the term grammar of CLI_REFERENCE 4.3/4.4 over the
// operator × key shape × value shape space.
func TestParseTerms(t *testing.T) {
	cases := []struct {
		in   string
		want []Term
	}{
		{"status=todo", []Term{{Key: Key{Name: "status"}, Op: OpEq, Values: []string{"todo"}}}},
		{"status:todo,in_progress", []Term{{Key: Key{Name: "status"}, Op: OpEq, Values: []string{"todo", "in_progress"}}}},
		{"client!=acme", []Term{{Key: Key{Name: "client"}, Op: OpNe, Values: []string{"acme"}}}},
		{"region~=EU-central", []Term{{Key: Key{Name: "region"}, Op: OpFold, Values: []string{"EU-central"}}}},
		{"cost>2", []Term{{Key: Key{Name: "cost"}, Op: OpGt, Values: []string{"2"}}}},
		{"cost>=2.5", []Term{{Key: Key{Name: "cost"}, Op: OpGe, Values: []string{"2.5"}}}},
		{"due_at<2026-12-01", []Term{{Key: Key{Name: "due_at"}, Op: OpLt, Values: []string{"2026-12-01"}}}},
		{"due_at<=2026-12-01T10:00:00Z", []Term{{Key: Key{Name: "due_at"}, Op: OpLe, Values: []string{"2026-12-01T10:00:00Z"}}}},
		{"jira?", []Term{{Key: Key{Name: "jira"}, Op: OpHas}}},
		{"jira!?", []Term{{Key: Key{Name: "jira"}, Op: OpMissing}}},
		{"has:estimate", []Term{{Key: Key{Name: "estimate"}, Op: OpHas}}},
		{"has:meta.estimate", []Term{{Key: Key{Name: "estimate", Meta: true}, Op: OpHas}}},
		{"has=x", []Term{{Key: Key{Name: "has"}, Op: OpEq, Values: []string{"x"}}}},
		{"meta.client=acme", []Term{{Key: Key{Name: "client", Meta: true}, Op: OpEq, Values: []string{"acme"}}}},
		{"meta=x", []Term{{Key: Key{Name: "meta"}, Op: OpEq, Values: []string{"x"}}}},
		{"a.b.c=x", []Term{{Key: Key{Name: "a.b.c"}, Op: OpEq, Values: []string{"x"}}}},
		// The operator is the first one after the key: value keeps the rest.
		{"formula=a>b", []Term{{Key: Key{Name: "formula"}, Op: OpEq, Values: []string{"a>b"}}}},
		{"x==y", []Term{{Key: Key{Name: "x"}, Op: OpEq, Values: []string{"=y"}}}},
		{"url=https://a.b/c?d=e", []Term{{Key: Key{Name: "url"}, Op: OpEq, Values: []string{"https://a.b/c?d=e"}}}},
		// Unicode and spaces in values are fine (quoted keeps commas/spaces).
		{"title=café", []Term{{Key: Key{Name: "title"}, Op: OpEq, Values: []string{"café"}}}},
		{`title="a, b c"`, []Term{{Key: Key{Name: "title"}, Op: OpEq, Values: []string{"a, b c"}}}},
		{`title~="日本 語"`, []Term{{Key: Key{Name: "title"}, Op: OpFold, Values: []string{"日本 語"}}}},
		// Several terms in one flag (XQ7).
		{"status:todo,in_progress priority>=high", []Term{
			{Key: Key{Name: "status"}, Op: OpEq, Values: []string{"todo", "in_progress"}},
			{Key: Key{Name: "priority"}, Op: OpGe, Values: []string{"high"}},
		}},
		{"  a=1 \t b!? ", []Term{{Key: Key{Name: "a"}, Op: OpEq, Values: []string{"1"}}, {Key: Key{Name: "b"}, Op: OpMissing}}},
		{"k9_x=1", []Term{{Key: Key{Name: "k9_x"}, Op: OpEq, Values: []string{"1"}}}},
	}
	for _, c := range cases {
		f, err := Parse([]string{c.in})
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		got := f.Terms
		for i := range got {
			got[i].Raw = ""
		}
		if !slices.EqualFunc(got, c.want, func(a, b Term) bool {
			return a.Key == b.Key && a.Op == b.Op && slices.Equal(a.Values, b.Values)
		}) {
			t.Errorf("%q: got %+v want %+v", c.in, got, c.want)
		}
	}
}

// TestParseRefused: every malformed input is an invalid argument (exit
// 20) with a message naming the problem, never an empty filter.
func TestParseRefused(t *testing.T) {
	cases := map[string]string{
		"":                             "empty filter",
		"   ":                          "empty filter",
		"todo":                         "no operator",
		"Status=todo":                  "lowercase ASCII",
		"stätus=todo":                  "lowercase ASCII",
		"日本=x":                         "lowercase ASCII",
		"=x":                           "starts with a key",
		"9a=x":                         "starts with a lowercase letter",
		"_a=x":                         "starts with a lowercase letter",
		"a..b=x":                       "starts with a lowercase letter",
		"a.=x":                         "starts with a lowercase letter",
		"a.b.c.d.e=x":                  "at most 4",
		"status=":                      "no value",
		"status:":                      "no value",
		"status=a,,b":                  "empty value",
		"status=a,":                    "empty value",
		"jira?x":                       "takes no value",
		"jira!?x":                      "takes no value",
		`title="a b`:                   "not closed",
		`title=a"b"`:                   "quote the whole value",
		`title="a"b`:                   "quote the whole value",
		`title="a"b"`:                  "not closed",
		"data$.a=1":                    "$ paths",
		"has:":                         "no value",
		"has:a,b":                      "one key",
		"has:Bad":                      "starts with a lowercase letter",
		strings.Repeat("a", 65) + "=x": "1 to 64",
	}
	for in, want := range cases {
		_, err := Parse([]string{in})
		if err == nil {
			t.Errorf("%q: parsed", in)
			continue
		}
		if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want E_INVALID_INPUT containing %q", in, err, want)
		}
	}
	if _, err := Parse([]string{strings.Repeat("a", 64) + "=x"}); err != nil {
		t.Errorf("64-char key: %v", err)
	}
}

// TestParseRepeated: repeated flags are ANDed into one term list; no flag
// is the empty filter.
func TestParseRepeated(t *testing.T) {
	f, err := Parse([]string{"a=1", "b=2 c=3"})
	if err != nil || len(f.Terms) != 3 {
		t.Fatalf("got %+v, %v", f, err)
	}
	f, err = Parse(nil)
	if err != nil || !f.Empty() {
		t.Fatalf("nil: %+v, %v", f, err)
	}
}

// fields is a test schema covering every comparison kind.
var fields = map[string]Field{
	"status":   {Values: []string{"todo", "in_progress", "done"}},
	"priority": {Values: []string{"low", "medium", "high", "urgent"}},
	"client":   {},
	"cost":     {Kind: value.KindNumber},
	"due_at":   {Kind: value.KindDateTime},
	"born":     {Kind: value.KindDate},
	"ok":       {Kind: value.KindBool},
	"took":     {Kind: value.KindDuration},
	"tags":     {Kind: value.KindList, Elem: value.KindText},
	"scores":   {Kind: value.KindList, Elem: value.KindNumber},
}

func resolve(k Key) (Field, error) {
	f, ok := fields[k.Name]
	if !ok || k.Meta {
		return Field{}, UnknownKey(k, []string{"status"})
	}
	return f, nil
}

// TestMatch is the kind × operator × row-shape matrix.
func TestMatch(t *testing.T) {
	type row map[string][]string
	cases := []struct {
		where string
		row   row
		want  bool
	}{
		{"status=todo", row{"status": {"todo"}}, true},
		{"status=todo", row{"status": {"done"}}, false},
		{"status=todo", row{}, false},
		{"status:todo,done", row{"status": {"done"}}, true},
		{"status!=todo", row{"status": {"done"}}, true},
		{"status!=todo,done", row{"status": {"done"}}, false},
		{"status!=todo", row{}, true},
		{"priority>=high", row{"priority": {"urgent"}}, true},
		{"priority>=high", row{"priority": {"high"}}, true},
		{"priority>=high", row{"priority": {"medium"}}, false},
		{"priority<medium", row{"priority": {"low"}}, true},
		{"priority>low", row{"priority": {"bogus"}}, false},
		{"client=acme", row{"client": {"Acme"}}, false},
		{"client~=acme", row{"client": {"Acme"}}, true},
		{"client~=CME", row{"client": {"Acme"}}, true},
		{"client~=x", row{}, false},
		{"client~=café", row{"client": {"CAFÉ du nord"}}, true},
		{"client?", row{"client": {"a"}}, true},
		{"client?", row{}, false},
		{"client!?", row{}, true},
		{"client!?", row{"client": {"a"}}, false},
		{"cost>2", row{"cost": {"3.5"}}, true},
		{"cost>2", row{"cost": {"10"}}, true}, // numeric, not text order
		{"cost>2", row{"cost": {"2"}}, false},
		{"cost>=2", row{"cost": {"2"}}, true},
		{"cost=2", row{"cost": {"2.0"}}, true},
		{"cost<0", row{"cost": {"-1"}}, true},
		{"cost>2", row{"cost": {"not-a-number"}}, false},
		{"due_at<2026-12-01", row{"due_at": {"2026-11-30T23:59:59.000000000Z"}}, true},
		{"due_at<2026-12-01", row{"due_at": {"2026-12-01T00:00:01.000000000Z"}}, false},
		{"due_at=2026-12-01", row{"due_at": {"2026-12-01T15:00:00.000000000Z"}}, true},
		{"due_at>2026-12-01T10:00:00Z", row{"due_at": {"2026-12-01T15:00:00.000000000Z"}}, true},
		{"due_at>2026-12-01T10:00:00+02:00", row{"due_at": {"2026-12-01T09:00:00.000000000Z"}}, true},
		{"born>=2000-01-01", row{"born": {"1999-12-31"}}, false},
		{"ok=true", row{"ok": {"true"}}, true},
		{"ok=false", row{"ok": {"true"}}, false},
		{"took>1h", row{"took": {"90m"}}, true},
		{"tags=b", row{"tags": {"a", "b"}}, true},
		{"tags=c", row{"tags": {"a", "b"}}, false},
		{"tags!=b", row{"tags": {"a", "b"}}, false},
		{"tags?", row{"tags": {}}, false},
		{"scores>5", row{"scores": {"1", "9"}}, true},
		{"scores>50", row{"scores": {"1", "9"}}, false},
		// AND across terms and across flags.
		{"status=todo priority>=high", row{"status": {"todo"}, "priority": {"low"}}, false},
		{"status=todo priority>=high", row{"status": {"todo"}, "priority": {"urgent"}}, true},
	}
	for _, c := range cases {
		f, err := Parse([]string{c.where})
		if err != nil {
			t.Fatalf("%q: %v", c.where, err)
		}
		m, err := f.Bind(resolve)
		if err != nil {
			t.Fatalf("%q bind: %v", c.where, err)
		}
		if got := m.Match(func(k Key) []string { return c.row[k.Name] }); got != c.want {
			t.Errorf("%q on %v: got %v want %v", c.where, c.row, got, c.want)
		}
	}
	var zero Matcher
	if !zero.Match(func(Key) []string { return nil }) {
		t.Error("the zero Matcher must match every row")
	}
}

// TestBindRefused: kind and value checks happen before any row is read.
func TestBindRefused(t *testing.T) {
	cases := map[string]string{
		"client>acme":     `">" compares numbers, dates and datetimes; client is text`,
		"tags<b":          "tags is text",
		"ok>true":         "ok is bool",
		"status=todoo":    `status "todoo" is not one of todo, in_progress, done`,
		"priority>=hi":    `priority "hi" is not one of`,
		"cost=abc":        `"abc" is not a number`,
		"cost>1,2":        "takes one value",
		"due_at<tomorrow": "want YYYY-MM-DD or RFC 3339",
		"born=2026-13-01": "want YYYY-MM-DD",
		"ok=yes":          "want true or false",
		"took>soon":       "want a duration",
		"nope=1":          "unknown field nope",
		"meta.status=x":   "unknown field meta.status",
	}
	for in, want := range cases {
		f, err := Parse([]string{in})
		if err != nil {
			t.Fatalf("%q parse: %v", in, err)
		}
		_, err = f.Bind(resolve)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v want %q", in, err, want)
		}
		var e *errs.Error
		if err != nil && (!errors.As(err, &e) || e.Code != errs.InvalidInput) {
			t.Errorf("%q: code %v", in, errs.CodeOf(err))
		}
	}
	// ~=, ? and !? never check the value kind.
	for _, ok := range []string{"cost~=1", "ok?", "due_at!?", "client~=x"} {
		f, _ := Parse([]string{ok})
		if _, err := f.Bind(resolve); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
}

// TestKeyString round-trips the meta. prefix for messages.
func TestKeyString(t *testing.T) {
	if (Key{Name: "c", Meta: true}).String() != "meta.c" || (Key{Name: "c"}).String() != "c" {
		t.Fatal("Key.String")
	}
	f, _ := Parse([]string{"a=1 meta.b?"})
	m, err := f.Bind(func(Key) (Field, error) { return Field{}, nil })
	if err != nil || !slices.Equal(m.Keys(), []Key{{Name: "a"}, {Name: "b", Meta: true}}) {
		t.Fatalf("Keys: %v %v", m.Keys(), err)
	}
}
