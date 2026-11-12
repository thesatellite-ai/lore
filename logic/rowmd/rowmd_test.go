package rowmd

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/validate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// testActor stamps the fixture rows.
const testActor = "act_e16a8fd67eb4732194ef0c9a4e9d478e"

// textCases are the strings every text field is written with: empty,
// unicode, multi-line, and the characters YAML gives a meaning (`: `, `#`,
// a leading `-`, `|`, `>`, quotes, brackets, words YAML reads as bool or
// null, numbers, a date, edge spaces and tabs).
var textCases = []string{
	"plain words",
	"",
	"émoji ✓ 日本語 — ünïcødé",
	"line one\nline two\n\nline four",
	"key: value # not a comment",
	"- starts like a list item",
	"| starts like a block",
	"> folded?",
	"'single' and \"double\" quotes",
	"[not, a, list]",
	"{not: a map}",
	"yes",
	"null",
	"~",
	"123",
	"1e5",
	"2026-11-12",
	"  leading spaces",
	"trailing spaces  ",
	"tab\tinside",
	"multi\n  indented\n\ttabbed\nend  ",
	"#hash first",
	"*star alias?",
	"&anchor?",
	"%percent",
	"@at",
	"`backtick`",
	"---",
	"a\n---\nb",
	strings.Repeat("a long line of words that goes on ", 8),
	"long block\n" + strings.Repeat("a long line of words that goes on ", 8) + "\nend",
}

// fixtureID is an id of def's table.
func fixtureID(t *testing.T, def schema.TableDef, n int) ids.AnyID {
	t.Helper()
	hex := "01a1cf02b365751bac5520b3d393eef"
	id, err := ids.ParseAny(string(def.Prefix) + "_" + hex + string("0123456789abcdef"[n%16]))
	if err != nil {
		t.Fatalf("id for %s: %v", def.Name, err)
	}
	return id
}

// sample is a valid stored value of f.
func sample(t *testing.T, def schema.TableDef, f schema.FieldDef) value.Value {
	t.Helper()
	var v value.Value
	var err error
	switch f.Kind {
	case value.KindText:
		if len(f.Values) > 0 {
			v, err = value.TextOf(f.Values[len(f.Values)-1])
		} else {
			v, err = value.TextOf("sample " + f.Name)
		}
	case value.KindNumber:
		v, err = value.NumberOf("42")
	case value.KindBool:
		v = value.BoolOf(true)
	case value.KindDate:
		v, err = value.DateOf("2026-11-12")
	case value.KindDateTime:
		v, err = value.DateTimeOf("2026-11-12T09:07:03Z")
	case value.KindDuration:
		v, err = value.DurationOf("1h30m0s")
	case value.KindIP:
		v, err = value.IPOf("203.0.113.10")
	case value.KindCIDR:
		v, err = value.CIDROf("10.0.0.0/8")
	case value.KindDNS:
		v, err = value.DNSOf("db.example.test")
	case value.KindURL:
		v, err = value.URLOf("https://example.test/a?b=c#d")
	case value.KindEmail:
		v, err = value.EmailOf("dev@example.test")
	case value.KindSecretRef:
		v, err = value.SecretRefOf("gopass://team/db")
	case value.KindJSON:
		v, err = value.JSONOf([]byte(`{"a":[1,2.5,"x: y"],"b":{"c":null,"d":true},"e":"line\nbreak","f":"123"}`))
	case value.KindRef:
		target := f.RefTable
		td, ok := tableDef(target)
		if !ok {
			td = def
		}
		v, err = value.RefOf(fixtureID(t, td, 3))
	case value.KindList:
		switch f.Elem {
		case value.KindRef:
			td, ok := tableDef(f.RefTable)
			if !ok {
				td = def
			}
			a, _ := value.RefOf(fixtureID(t, td, 4))
			b, _ := value.RefOf(fixtureID(t, td, 5))
			v, err = value.ListOf(value.KindRef, a, b)
		case value.KindNumber:
			v, err = value.ListOf(value.KindNumber, value.Int(1), value.Int(2))
		default:
			a, _ := value.TextOf("one: 1")
			b, _ := value.TextOf("two")
			v, err = value.ListOf(value.KindText, a, b)
		}
	case value.KindNull:
		v = value.Null{}
	}
	if err != nil {
		t.Fatalf("sample %s.%s: %v", def.Name, f.Name, err)
	}
	return v
}

// stored builds a row of def with fields, as a backend hands it back
// (encoded and decoded, so its fields are in their inferred form).
func stored(t *testing.T, def schema.TableDef, fields map[string]value.Value) row.Row {
	t.Helper()
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 12, 9, 7, 3, 0, time.UTC)
	r := row.Row{Table: def.Name, ID: fixtureID(t, def, 1), HLC: hlc.HLC{Wall: at, Counter: 1, Actor: actor},
		CreatedAt: at, CreatedBy: actor, UpdatedAt: at, UpdatedBy: actor, Fields: fields}
	raw, err := row.Encode(r)
	if err != nil {
		t.Fatalf("encode %s: %v", def.Name, err)
	}
	back, err := row.Decode(raw)
	if err != nil {
		t.Fatalf("decode %s: %v", def.Name, err)
	}
	return back
}

// fullRow is a row of def with every field set.
func fullRow(t *testing.T, def schema.TableDef) row.Row {
	t.Helper()
	fields := map[string]value.Value{}
	for _, f := range def.Fields {
		fields[f.Name] = sample(t, def, f)
	}
	return stored(t, def, fields)
}

// builtin is schema.Builtin, built once.
var builtin = schema.Builtin()

// tableDef looks a built-in table up.
func tableDef(name row.Table) (schema.TableDef, bool) {
	for _, d := range builtin {
		if d.Name == name {
			return d, true
		}
	}
	return schema.TableDef{}, false
}

// tables are the built-in synced tables (every kind of row a person edits).
func tables() []schema.TableDef {
	var out []schema.TableDef
	for _, d := range builtin {
		if d.Synced {
			out = append(out, d)
		}
	}
	return out
}

func render(t *testing.T, def schema.TableDef, r row.Row) []byte {
	t.Helper()
	b, err := Render(def, r, Where{Store: "repo", Path: "/tmp/x/" + r.ID.String() + ".json"})
	if err != nil {
		t.Fatalf("render %s: %v", def.Name, err)
	}
	return b
}

// TestUnchangedViewIsNoChange: every table, full and empty rows: the view
// read back unchanged changes nothing.
func TestUnchangedViewIsNoChange(t *testing.T) {
	t.Parallel()
	for _, def := range tables() {
		for name, r := range map[string]row.Row{"full": fullRow(t, def), "empty": stored(t, def, map[string]value.Value{})} {
			view := render(t, def, r)
			res, err := Parse(def, r, "view.md", view)
			if err != nil {
				t.Errorf("%s %s: unchanged view refused: %v\n%s", def.Name, name, err, view)
				continue
			}
			if !res.NoChange() {
				t.Errorf("%s %s: unchanged view changes %v\n%s", def.Name, name, res.Changed, view)
			}
		}
	}
}

// TestEditEveryTextField: every text field of every table (and the body),
// written with every textCases string, reads back as exactly that string
// and nothing else changes.
func TestEditEveryTextField(t *testing.T) {
	t.Parallel()
	for _, def := range tables() {
		t.Run(string(def.Name), func(t *testing.T) {
			t.Parallel()
			editTextFields(t, def)
		})
	}
}

// editTextFields is TestEditEveryTextField for one table.
func editTextFields(t *testing.T, def schema.TableDef) {
	{
		cur := fullRow(t, def)
		for _, f := range def.Fields {
			if f.Kind != value.KindText || len(f.Values) > 0 || f.LoreRecord {
				continue
			}
			for _, s := range textCases {
				want, wantNull := expectText(t, def, f, s)
				if wantNull && f.Required {
					continue // a required field cannot be emptied (TestRefusals)
				}
				edited := withField(t, def, cur, f.Name, s)
				res, err := Parse(def, cur, "view.md", render(t, def, edited))
				if err != nil {
					t.Errorf("%s.%s = %q: refused: %v", def.Name, f.Name, s, err)
					continue
				}
				if len(res.Changed) != 1 || res.Changed[0] != f.Name {
					t.Errorf("%s.%s = %q: changed %v, want only %s", def.Name, f.Name, s, res.Changed, f.Name)
					continue
				}
				got := res.Patch[f.Name]
				if wantNull {
					if got.Kind() != value.KindNull {
						t.Errorf("%s.%s = %q: got %v, want cleared", def.Name, f.Name, s, got)
					}
					continue
				}
				if got.String() != want {
					t.Errorf("%s.%s = %q: got %q, want %q", def.Name, f.Name, s, got.String(), want)
				}
			}
		}
	}
}

// expectText is what a write of s into f stores (validate trims and cleans
// some fields); wantNull when that is empty.
func expectText(t *testing.T, def schema.TableDef, f schema.FieldDef, s string) (string, bool) {
	t.Helper()
	in, _ := value.TextOf(s)
	got, err := validate.Fields(def, map[string]value.Value{f.Name: in}, validate.Patch)
	if err != nil {
		return "", true
	}
	w := got[f.Name].String()
	return w, w == ""
}

// withField is cur with one text field set (or cleared for "").
func withField(t *testing.T, def schema.TableDef, cur row.Row, name, s string) row.Row {
	t.Helper()
	fields := map[string]value.Value{}
	for k, v := range cur.Fields {
		fields[k] = v
	}
	if s == "" {
		delete(fields, name)
	} else {
		fields[name], _ = value.TextOf(s)
	}
	r := stored(t, def, fields)
	return r
}

// TestEditEveryKind: each non-text kind changed to another valid value
// reads back as that value.
func TestEditEveryKind(t *testing.T) {
	t.Parallel()
	def, _ := tableDef(schema.TableTasks)
	cur := fullRow(t, def)
	cases := []struct{ field, line, want string }{
		{"timebox_seconds", "timebox_seconds: 1800", "1800"},
		{"due_at", "due_at: 2026-12-01T10:00:00Z", "2026-12-01T10:00:00"},
		{"due_at", "due_at:", ""},
		{"status", "status: done", "done"},
		{"blocked_by", `blocked_by: [{"id": "tsk_01a1cf02b365751bac5520b3d393eef9", "on": "done"}]`, `[{"id":"tsk_01a1cf02b365751bac5520b3d393eef9","on":"done"}]`},
		{"assignees", "assignees: [" + testActor + "]", testActor},
	}
	for _, c := range cases {
		if _, ok := def.Field(c.field); !ok {
			t.Fatalf("tasks has no field %s; fix the test", c.field)
		}
		view := replaceLine(t, render(t, def, cur), c.field, c.line)
		res, err := Parse(def, cur, "view.md", view)
		if err != nil {
			t.Errorf("%s: refused: %v\n%s", c.line, err, view)
			continue
		}
		got, ok := res.Patch[c.field]
		if !ok || len(res.Patch) != 1 {
			t.Errorf("%s: patch %v, want only %s", c.line, res.Changed, c.field)
			continue
		}
		s := got.String()
		if raw, isJSON := value.JSONForm(got); isJSON && (got.Kind() == value.KindJSON) {
			s = string(raw)
		}
		if c.want == "" {
			if got.Kind() != value.KindNull {
				t.Errorf("%s: got %v, want cleared", c.line, got)
			}
			continue
		}
		if !strings.Contains(s, c.want) {
			t.Errorf("%s: got %s, want %s", c.line, s, c.want)
		}
	}
}

// replaceLine swaps the frontmatter line of key for line.
func replaceLine(t *testing.T, view []byte, key, line string) []byte {
	t.Helper()
	ls := strings.Split(string(view), "\n")
	for i, l := range ls {
		if strings.HasPrefix(l, key+":") {
			ls[i] = line
			// drop a block value's indented lines
			j := i + 1
			for j < len(ls) && strings.HasPrefix(ls[j], "  ") {
				j++
			}
			return []byte(strings.Join(append(ls[:i+1], ls[j:]...), "\n"))
		}
	}
	t.Fatalf("no line %s: in\n%s", key, view)
	return nil
}

// lineOf is the 1-based line of the first line starting with prefix.
func lineOf(t *testing.T, view []byte, prefix string) int {
	t.Helper()
	for i, l := range strings.Split(string(view), "\n") {
		if strings.HasPrefix(l, prefix) {
			return i + 1
		}
	}
	t.Fatalf("no line %q in\n%s", prefix, view)
	return 0
}

// TestRefusals: each refusal names file:line and why, and nothing is
// written.
func TestRefusals(t *testing.T) {
	t.Parallel()
	def, _ := tableDef(schema.TableTasks)
	cur := fullRow(t, def)
	view := render(t, def, cur)
	edit := func(key, line string) []byte { return replaceLine(t, view, key, line) }
	cases := []struct {
		name string
		text []byte
		code errs.Code
		line int
		says []string
	}{
		{"id changed", edit("id", "id: tsk_ffffffffffffffffffffffffffffffff"), errs.InvalidInput, lineOf(t, view, "id:"), []string{"id cannot change", "names the row's file"}},
		{"table changed", edit("table", "table: memories"), errs.InvalidInput, lineOf(t, view, "table:"), []string{"table cannot change"}},
		{"created_at changed", edit("created_at", "created_at: 2020-01-01T00:00:00Z"), errs.InvalidInput, lineOf(t, view, "created_at:"), []string{"created_at cannot change", "written once"}},
		{"updated_by changed", edit("updated_by", "updated_by: act_ffffffffffffffffffffffffffffffff"), errs.InvalidInput, lineOf(t, view, "updated_by:"), []string{"updated_by cannot change"}},
		{"hlc changed", edit("_hlc", "_hlc: 2020-01-01T00:00:00.000Z/0001/"+testActor), errs.Stale, lineOf(t, view, "_hlc:"), []string{"was written since this view was made"}},
		{"unknown key", edit("priority", "priority: high\nprioritee: low"), errs.InvalidInput, lineOf(t, view, "priority:") + 1, []string{"unknown key prioritee", "tasks has no field prioritee"}},
		{"bad number", edit("timebox_seconds", "timebox_seconds: lots"), errs.InvalidInput, lineOf(t, view, "timebox_seconds:"), []string{"timebox_seconds", "a number"}},
		{"bad date", edit("due_at", "due_at: next tuesday-ish"), errs.InvalidInput, lineOf(t, view, "due_at:"), []string{"due_at", "RFC 3339"}},
		{"bad choice", edit("status", "status: maybe"), errs.InvalidInput, lineOf(t, view, "status:"), []string{"status"}},
		{"required emptied", edit("title", "title:"), errs.InvalidInput, lineOf(t, view, "title:"), []string{"title", "required"}},
		{"list as text", edit("assignees", "assignees: someone"), errs.InvalidInput, lineOf(t, view, "assignees:"), []string{"assignees", "want a list"}},
		{"lore record changed", edit("time_open", `time_open: {"by": "someone"}`), errs.InvalidInput, lineOf(t, view, "time_open:"), []string{"time_open cannot change", "lore writes this record itself"}},
		{"removed key", edit("title", ""), errs.InvalidInput, 1, []string{"the key title was removed", "`title:`"}},
		{"duplicate key", edit("priority", "priority: high\npriority: low"), errs.InvalidInput, lineOf(t, view, "priority:") + 1, []string{"appears twice"}},
		{"bad yaml", edit("priority", "priority: [high"), errs.InvalidInput, 0, []string{"not valid YAML"}},
		{"no opening line", []byte(strings.TrimPrefix(string(view), "---\n")), errs.InvalidInput, 1, []string{"must start with a line `---`"}},
		{"not closed", []byte(strings.Replace(string(view), "\n---\n", "\n", 1)), errs.InvalidInput, 1, []string{"not closed"}},
		{"body as key", edit("priority", "priority: high\nbody: text"), errs.InvalidInput, lineOf(t, view, "priority:") + 1, []string{"body is the text below the closing ---"}},
	}
	for _, c := range cases {
		res, err := Parse(def, cur, "/tmp/edit-tsk.md", c.text)
		if err == nil {
			t.Errorf("%s: accepted (patch %v), want a refusal", c.name, res.Changed)
			continue
		}
		if !errs.Has(err, c.code) {
			t.Errorf("%s: code %s, want %s: %v", c.name, errs.CodeOf(err), c.code, err)
		}
		msg := errs.EnvelopeOf(err).Error.Message
		if c.line > 0 {
			want := "/tmp/edit-tsk.md:" + itoa(c.line) + ": "
			if !strings.Contains(msg, want) {
				t.Errorf("%s: message lacks %q:\n%s", c.name, want, msg)
			}
		} else if !regexp.MustCompile(`/tmp/edit-tsk\.md:\d+: `).MatchString(msg) {
			t.Errorf("%s: message names no file:line:\n%s", c.name, msg)
		}
		for _, s := range c.says {
			if !strings.Contains(msg, s) {
				t.Errorf("%s: message lacks %q:\n%s", c.name, s, msg)
			}
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// TestNoBodyTable: a table without a long text field takes no body.
func TestNoBodyTable(t *testing.T) {
	t.Parallel()
	def, _ := tableDef(schema.TableTags)
	if BodyField(def) != "" {
		t.Skip("tags has a body field now")
	}
	cur := fullRow(t, def)
	view := append(render(t, def, cur), []byte("stray text\n")...)
	_, err := Parse(def, cur, "v.md", view)
	if err == nil || !strings.Contains(err.Error(), "has no long text field") {
		t.Fatalf("body text in a table without one: %v", err)
	}
}

// TestBodyFieldPerTable pins which field is the body.
func TestBodyFieldPerTable(t *testing.T) {
	t.Parallel()
	want := map[row.Table]string{
		schema.TableTasks: "body", schema.TableMemories: "body", schema.TableReminders: "message",
		schema.TableActors: "bio", schema.TableTechDocs: "description", schema.TableTags: "",
	}
	for table, w := range want {
		def, ok := tableDef(table)
		if !ok {
			t.Fatalf("no table %s", table)
		}
		if got := BodyField(def); got != w {
			t.Errorf("BodyField(%s) = %q, want %q", table, got, w)
		}
	}
}
