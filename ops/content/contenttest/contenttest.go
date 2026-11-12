// Package contenttest is the shared conformance check of the content
// nouns (ops/pattern, ops/playbook, ops/prompt and the twelve content-2
// nouns): one table of v0.1.17 facts per noun (badge, empty line, --json
// kinds and key order, edit line, which verbs exist) drives the same
// add / list / show / edit / archive / delete / search walk over a real
// runner, with an input matrix of titles and bodies (plain, unicode,
// huge, SQL and FTS reserved words, padded).
//
// Why a package and not a _test.go helper: fifteen noun packages run the
// same walk; a test-only helper cannot be shared across packages. It
// imports only op, model and schema (layer ops), never the harness that
// builds the runner (internal/opstest imports wire), so production code
// could import it without breaking the layer guard; nothing but tests
// does.
//
// Every expected value in a Spec was read from v0.1.17's real output
// (~/.local/bin/lore, `lore <noun> … [--json]` in a scratch
// repo, 2026-11-04), not from the code under test.
package contenttest

import (
	"bytes"
	"encoding/json"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/content"
	"github.com/thesatellite-ai/lore/schema"
)

// Runner runs an operation by name with a JSON input: internal/opstest.H
// implements it over a real wire.App.
type Runner interface {
	// Run runs as a caller that did not confirm.
	Run(name, input string) (op.Result, error)
	// RunYes runs with --yes.
	RunYes(name, input string) (op.Result, error)
}

// Mark is v0.1.17's success mark.
const Mark = "✓"

// DeletedText is a soft delete's line (content.DeletedText; DQ8).
func DeletedText(id string) string {
	return Mark + " " + id + " moved to the trash (restore: lore trash restore " + id + ")\n"
}

// Spec is one noun's v0.1.17 facts.
type Spec struct {
	// Noun is the op name prefix ("pattern" → pattern.add).
	Noun string
	// Table is the noun's core table (its FTS fields drive --column).
	Table row.Table
	// Prefix is the id prefix ("pat").
	Prefix string
	// KindList, KindShow and KindSearch are the --json envelope kinds.
	KindList, KindShow, KindSearch string
	// Empty is the human list line with no rows ("(no patterns)\n").
	Empty string
	// Add builds the add input for a title (ignored when the noun has
	// none) and a body.
	Add func(title, body string) string
	// TitleKey is the --json key holding the title ("title", "name";
	// "" for a noun without one).
	TitleKey string
	// AddLine is the human add line; nil means "✓ <id>\n".
	AddLine func(id, title string) string
	// ListLine is one human list line from the row's --json record (the
	// data object); nil means "<Badge>  <id>\n". TitleLine builds the
	// content.ListLine form ("<Badge>  <id>  <title>").
	ListLine func(t testing.TB, rec string) string
	// ShowText is the human show text from the row's --json record; nil
	// means content.ShowText (badge line, title or name, body).
	ShowText func(t testing.TB, rec string) string
	// SearchLine is one human search line; nil means "<id> <title>\n".
	SearchLine func(id, title, body string) string
	// Badge is v0.1.17's two-letter list badge.
	Badge string
	// RecordKeys is the v0.1.17 key order of a row added with only the
	// add fields (list --json data[0]).
	RecordKeys []string
	// HitKeys is the key order of a search hit's row object.
	HitKeys []string
	// EditWord is the word of the edit line ("✓ <word> <id> updated");
	// "" when the noun has no edit verb.
	EditWord string
	// EditTitle builds an edit input changing the title (or, for a noun
	// without one, the body) of id to v.
	EditTitle func(id, v string) string
	// Archive: the noun has archive/unarchive verbs.
	Archive bool
}

// Case is one row of the add input matrix.
type Case struct {
	Name, Title, Body string
}

// Stored is the case as a row stores it under titleKey (Spec.TitleKey):
// a title and the body trimmed of edge whitespace (OWNERQ-3, logic/validate
// trimmedFields), inner whitespace and every other byte kept; a name key
// (playbook, prompt, behaviour) keeps its whitespace as names do.
func (c Case) Stored(titleKey string) (title, body string) {
	title = c.Title
	if titleKey == "title" {
		title = strings.TrimSpace(title)
	}
	return title, strings.TrimSpace(c.Body)
}

// Matrix is the add input matrix every noun runs: each must be accepted
// and read back byte for byte, edges trimmed (Case.Stored).
func Matrix() []Case {
	return []Case{
		{Name: "plain", Title: "Retry wrapper", Body: "Wrap calls in retry.Do"},
		{Name: "unicode", Title: "héllo 世界 🚀", Body: "naïve café — 日本語 ✓ emoji 🚀"},
		{Name: "sql reserved", Title: "SELECT * FROM t; DROP TABLE x --", Body: "'; DELETE FROM patterns; -- null NULL"},
		{Name: "fts operators", Title: "NOT AND OR NEAR", Body: `"quoted" prefix* AND OR NOT ( ) ^`},
		{Name: "json-ish", Title: "null", Body: `{"a":[1,2,{"b":null}]}`},
		{Name: "padded", Title: "  padded  ", Body: "  edges trimmed,\n\n  inner  whitespace kept  \n\n"},
		{Name: "huge", Title: strings.Repeat("t", 300), Body: strings.Repeat("0123456789abcdef", 4096)},
	}
}

// Quote JSON-encodes s.
func Quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return `""`
	}
	return strings.TrimSpace(b.String())
}

// Text is a result's human form (op.Texter), "" when it has none.
func Text(res op.Result) string {
	v := res.Value()
	if !v.IsValid() {
		return ""
	}
	if t, ok := v.Interface().(op.Texter); ok {
		return t.Text()
	}
	return ""
}

// JSON is a result's JSON.
func JSON(t testing.TB, res op.Result) string {
	t.Helper()
	b, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Keys returns the keys of the JSON object at path (a list of object keys
// and array indexes as strings) in document order.
func Keys(t testing.TB, doc string, path ...string) []string {
	t.Helper()
	raw := json.RawMessage(doc)
	for _, p := range path {
		if i, err := strconv.Atoi(p); err == nil {
			var arr []json.RawMessage
			if err := json.Unmarshal(raw, &arr); err != nil || i >= len(arr) {
				t.Fatalf("path %v: no index %d in %s", path, i, raw)
			}
			raw = arr[i]
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("path %v: %v", path, err)
		}
		raw = m[p]
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		t.Fatalf("path %v: %v", path, err)
	}
	var keys []string
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("path %v: %v", path, err)
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		case string:
			if depth == 0 {
				keys = append(keys, v)
				// skip the value
				var skip json.RawMessage
				if err := dec.Decode(&skip); err != nil {
					t.Fatalf("path %v: %v", path, err)
				}
			}
		}
		if depth < 0 {
			break
		}
	}
	return keys
}

// Str reads a string at path ("" when absent).
func Str(t testing.TB, doc string, path ...string) string {
	t.Helper()
	raw := json.RawMessage(doc)
	for _, p := range path {
		if i, err := strconv.Atoi(p); err == nil {
			var arr []json.RawMessage
			if err := json.Unmarshal(raw, &arr); err != nil || i >= len(arr) {
				return ""
			}
			raw = arr[i]
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return ""
		}
		raw = m[p]
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// Fails asserts err carries code and returns its message.
func Fails(t testing.TB, what string, err error, code errs.Code) string {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error, want %s", what, code)
		return ""
	}
	if !errs.Has(err, code) {
		t.Fatalf("%s: %v, want %s", what, err, code)
	}
	return err.Error()
}

// fakeID is a well-formed id of prefix p that no row has.
func fakeID(p string) string { return p + "_01a1a5aca7f57659be7320c5f4b7ffff" }

// Check runs the whole conformance walk of s over a fresh runner made by
// newRunner (each sub-test gets its own repo).
func Check(t *testing.T, s Spec, newRunner func(t *testing.T) Runner) {
	t.Run("empty", func(t *testing.T) { checkEmpty(t, s, newRunner(t)) })
	t.Run("add matrix", func(t *testing.T) { checkAddMatrix(t, s, newRunner(t)) })
	t.Run("add refusals", func(t *testing.T) { checkAddRefusals(t, s, newRunner(t)) })
	t.Run("ids", func(t *testing.T) { checkIDs(t, s, newRunner(t)) })
	if s.EditWord != "" {
		t.Run("edit", func(t *testing.T) { checkEdit(t, s, newRunner(t)) })
	}
	t.Run("search", func(t *testing.T) { checkSearch(t, s, newRunner(t)) })
	if s.Archive {
		t.Run("archive", func(t *testing.T) { checkArchive(t, s, newRunner(t)) })
	}
	t.Run("delete", func(t *testing.T) { checkDelete(t, s, newRunner(t)) })
}

func must(t testing.TB, r Runner, name, in string) op.Result {
	t.Helper()
	res, err := r.Run(name, in)
	if err != nil {
		t.Fatalf("%s %.200s: %v", name, in, err)
	}
	return res
}

// add adds one row and returns its id, checking the add line.
func add(t testing.TB, s Spec, r Runner, title, body string) string {
	t.Helper()
	res := must(t, r, s.Noun+".add", s.Add(title, body))
	id := Str(t, JSON(t, res), "id")
	if !strings.HasPrefix(id, s.Prefix+"_") {
		t.Fatalf("add: id %q, want prefix %s_", id, s.Prefix)
	}
	if got := Str(t, JSON(t, res), "store"); got != "repo" {
		t.Errorf("add: store %q, want repo", got)
	}
	want := Mark + " " + id + "\n"
	if s.AddLine != nil {
		want = s.AddLine(id, title)
	}
	if got := Text(res); got != want {
		t.Errorf("add text %q, want %q", got, want)
	}
	return id
}

func checkEmpty(t *testing.T, s Spec, r Runner) {
	res := must(t, r, s.Noun+".list", "")
	if got := Text(res); got != s.Empty {
		t.Errorf("empty list text %q, want %q", got, s.Empty)
	}
	// v0.1.17: count is omitted at zero, data is [].
	want := `{"schema_version":1,"kind":"` + s.KindList + `","data":[]}`
	if got := JSON(t, res); got != want {
		t.Errorf("empty list json %s, want %s", got, want)
	}
}

func checkAddMatrix(t *testing.T, s Spec, r Runner) {
	var added []string
	titles := map[string]string{}
	for _, c := range Matrix() {
		id := add(t, s, r, c.Title, c.Body)
		added = append(added, id)
		wantTitle, wantBody := c.Stored(s.TitleKey)
		titles[id] = wantTitle
		show := JSON(t, must(t, r, s.Noun+".show", `{"id":"`+id+`"}`))
		if Str(t, show, "kind") != s.KindShow {
			t.Errorf("%s: show kind %q, want %q", c.Name, Str(t, show, "kind"), s.KindShow)
		}
		if got := Str(t, show, "data", "body"); got != wantBody {
			t.Errorf("%s: body not read back byte for byte (got %d bytes, want %d)", c.Name, len(got), len(wantBody))
		}
		if s.TitleKey != "" {
			if got := Str(t, show, "data", s.TitleKey); got != wantTitle {
				t.Errorf("%s: %s %q, want %q", c.Name, s.TitleKey, got, wantTitle)
			}
		}
	}
	list := must(t, r, s.Noun+".list", "")
	doc := JSON(t, list)
	var env struct {
		Count int               `json:"count"`
		Data  []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(doc), &env); err != nil {
		t.Fatal(err)
	}
	if env.Count != len(added) || len(env.Data) != len(added) {
		t.Fatalf("list count %d data %d, want %d", env.Count, len(env.Data), len(added))
	}
	var lines []string
	for _, raw := range env.Data {
		id := Str(t, string(raw), "id")
		if !slices.Contains(added, id) {
			t.Errorf("list has unknown id %s", id)
		}
		if s.TitleKey != "" && Str(t, string(raw), s.TitleKey) != titles[id] {
			t.Errorf("list %s: %s %q, want %q", id, s.TitleKey, Str(t, string(raw), s.TitleKey), titles[id])
		}
		if s.ListLine != nil {
			lines = append(lines, s.ListLine(t, string(raw)))
		} else {
			lines = append(lines, s.Badge+"  "+id+"\n")
		}
	}
	if got, want := Text(list), strings.Join(lines, ""); got != want {
		t.Errorf("list text\n%q\nwant\n%q", got, want)
	}
	if got := Keys(t, doc); !slices.Equal(got, []string{"schema_version", "kind", "count", "data"}) {
		t.Errorf("list envelope keys %v", got)
	}
	// INTEG-AUDIT-1: every record ends with the envelope's writers after
	// v0.1.17's keys, on every content noun (no per-noun list to forget).
	wantKeys := append(slices.Clone(s.RecordKeys), AuditKeys...)
	if got := Keys(t, doc, "data", "0"); !slices.Equal(got, wantKeys) {
		t.Errorf("record keys\n%v\nwant v0.1.17's + audit\n%v", got, wantKeys)
	}
	show := must(t, r, s.Noun+".show", `{"id":"`+added[0]+`"}`)
	if got := Keys(t, JSON(t, show)); !slices.Equal(got, []string{"schema_version", "kind", "data"}) {
		t.Errorf("show envelope keys %v", got)
	}
	if got := Keys(t, JSON(t, show), "data"); !slices.Equal(got, wantKeys) {
		t.Errorf("show record keys %v, want %v", got, wantKeys)
	}
	// INTEG-KNOWLEDGE-3: the default show text is the badge line, the
	// title or name and the body (content.ShowText), not the bare badge line.
	title := ""
	if s.TitleKey != "" {
		title = Str(t, JSON(t, show), "data", s.TitleKey)
	}
	want := content.ShowText(s.Badge, added[0], s.TitleKey, title, Str(t, JSON(t, show), "data", "body"))
	if s.ShowText != nil {
		var sh struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(JSON(t, show)), &sh); err != nil {
			t.Fatal(err)
		}
		want = s.ShowText(t, string(sh.Data))
	}
	// INTEG-AUDIT-2: the main block ends with the writers line
	want += WriterLine(t, JSON(t, show), "data")
	if got := Text(show); got != want {
		t.Errorf("show text %q, want %q", got, want)
	}
}

func checkAddRefusals(t *testing.T, s Spec, r Runner) {
	cases := map[string]struct {
		in   string
		code errs.Code
	}{
		"empty body":      {s.Add("T", ""), errs.EmptyBody},
		"blank body":      {s.Add("T", " \n\t "), errs.EmptyBody},
		"unknown field":   {`{"bogus":1}`, errs.InvalidInput},
		"secret in body":  {s.Add("T", "key = "+"sk_live_"+strings.Repeat("A", 24)+" and ghp_"+strings.Repeat("a", 36)), errs.NeedsConfirm},
		"unknown project": {strings.TrimSuffix(s.Add("T", "b"), "}") + `,"project":"no-such-project"}`, errs.NotFound},
		"unknown store":   {strings.TrimSuffix(s.Add("T", "b"), "}") + `,` + storeKey(s) + `:"nostore"}`, errs.NotFound},
	}
	if s.TitleKey != "" {
		cases["no title"] = struct {
			in   string
			code errs.Code
		}{s.Add("", "b"), errs.InvalidInput}
		cases["blank title"] = struct {
			in   string
			code errs.Code
		}{s.Add("   ", "b"), errs.InvalidInput}
	}
	for name, c := range cases {
		_, err := r.Run(s.Noun+".add", c.in)
		Fails(t, name, err, c.code)
	}
	// both the argument and --body: refused, not concatenated.
	both := strings.TrimSuffix(s.Add("T", "one"), "}") + `,"text":"two"}`
	_, err := r.Run(s.Noun+".add", both)
	Fails(t, "body twice", err, errs.InvalidInput)
	// nothing was written by any refusal
	if got := Text(must(t, r, s.Noun+".list", "")); got != s.Empty {
		t.Errorf("a refused add wrote a row: %q", got)
	}
	// the positional text alone works
	res := must(t, r, s.Noun+".add", strings.Replace(s.Add("T", "via arg"), `"body":`, `"text":`, 1))
	id := Str(t, JSON(t, res), "id")
	if got := Str(t, JSON(t, must(t, r, s.Noun+".show", `{"id":"`+id+`"}`)), "data", "body"); got != "via arg" {
		t.Errorf("positional body %q", got)
	}
}

// storeKey is the add input key naming the target store: --to, except
// for handoff where --to is the --for alias and --store names the store.
func storeKey(s Spec) string {
	if s.Noun == "handoff" {
		return `"store"`
	}
	return `"to"`
}

func checkIDs(t *testing.T, s Spec, r Runner) {
	other := "pat"
	if s.Prefix == other {
		other = "rul"
	}
	for _, verb := range []string{"show", "delete"} {
		for name, c := range map[string]struct {
			id   string
			code errs.Code
		}{
			"empty": {"", errs.InvalidInput},
			// a lookup id that cannot name a row is E_INVALID_ID; only a
			// well-formed unknown id is E_NOT_FOUND (B-ERRS-1, owner
			// BATCH2 #11, ops/internal/lookup)
			"garbage":      {"not-an-id", errs.InvalidID},
			"wrong prefix": {fakeID(other), errs.InvalidID},
			"huge":         {strings.Repeat("z", 5000), errs.InvalidID},
			"all zeros":    {string(s.Prefix) + "_" + strings.Repeat("0", 32), errs.InvalidID},
			"unknown":      {fakeID(s.Prefix), errs.NotFound},
		} {
			_, err := r.RunYes(s.Noun+"."+verb, `{"id":`+Quote(c.id)+`}`)
			msg := Fails(t, verb+" "+name, err, c.code)
			if len(msg) > 400 {
				t.Errorf("%s %s: message repeats the whole input (%d bytes)", verb, name, len(msg))
			}
			if name == "unknown" && !strings.Contains(msg, s.Noun+` "`+c.id+`" not found`) {
				t.Errorf("%s unknown: message %q, want v0.1.17's `%s \"<id>\" not found`", verb, msg, s.Noun)
			}
		}
	}
}

func checkEdit(t *testing.T, s Spec, r Runner) {
	id := add(t, s, r, "Old title", "old body")
	_, err := r.Run(s.Noun+".edit", `{"id":"`+id+`"}`)
	if msg := Fails(t, "edit nothing", err, errs.InvalidInput); !strings.Contains(msg, "nothing to change") {
		t.Errorf("edit nothing: %q", msg)
	}
	res := must(t, r, s.Noun+".edit", s.EditTitle(id, "New ✓ title"))
	if got, want := Text(res), Mark+" "+s.EditWord+" "+id+" updated\n"; got != want {
		t.Errorf("edit text %q, want %q", got, want)
	}
	show := JSON(t, must(t, r, s.Noun+".show", `{"id":"`+id+`"}`))
	key := s.TitleKey
	if key == "" {
		key = "body"
	}
	if got := Str(t, show, "data", key); got != "New ✓ title" {
		t.Errorf("after edit %s = %q", key, got)
	}
	if s.TitleKey != "" && Str(t, show, "data", "body") != "old body" {
		t.Errorf("edit of the title changed the body: %q", Str(t, show, "data", "body"))
	}
	_, err = r.Run(s.Noun+".edit", s.EditTitle(fakeID(s.Prefix), "x"))
	Fails(t, "edit unknown", err, errs.NotFound)
	_, err = r.Run(s.Noun+".edit", s.EditTitle(id, "x sk_live_"+strings.Repeat("B", 24)))
	// a secret asks first (the engine's scan): a caller that cannot
	// answer gets E_NEEDS_CONFIRM and nothing changes.
	Fails(t, "edit secret", err, errs.NeedsConfirm)
	if got := Str(t, JSON(t, must(t, r, s.Noun+".show", `{"id":"`+id+`"}`)), "data", key); got != "New ✓ title" {
		t.Errorf("a refused edit changed %s to %q", key, got)
	}
}

func checkSearch(t *testing.T, s Spec, r Runner) {
	id1 := add(t, s, r, "Retry wrapper", "wrap calls in retry with backoff")
	id2 := add(t, s, r, "Logging", "use slog everywhere and retry nothing")
	add(t, s, r, "Unrelated", "nothing here")
	res := must(t, r, s.Noun+".search", `{"query":"retry"}`)
	doc := JSON(t, res)
	if got := Keys(t, doc); !slices.Equal(got, []string{"schema_version", "kind", "query", "count", "data"}) {
		t.Errorf("search envelope keys %v", got)
	}
	if Str(t, doc, "kind") != s.KindSearch || Str(t, doc, "query") != "retry" {
		t.Errorf("search kind/query: %s", doc)
	}
	var env struct {
		Count int `json:"count"`
		Data  []struct {
			ID      string  `json:"id"`
			Score   float64 `json:"score"`
			Snippet string  `json:"snippet"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(doc), &env); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range env.Data {
		got = append(got, d.ID)
		if !strings.Contains(strings.ToLower(d.Snippet), "<b>retry</b>") {
			t.Errorf("hit %s snippet %q has no <b> marks around the match", d.ID, d.Snippet)
		}
	}
	slices.Sort(got)
	want := []string{id1, id2}
	slices.Sort(want)
	if env.Count != 2 || !slices.Equal(got, want) {
		t.Fatalf("search retry: count %d ids %v, want %v", env.Count, got, want)
	}
	if k := Keys(t, doc, "data", "0"); !slices.Equal(k, []string{"id", "score", "snippet", "row"}) {
		t.Errorf("hit keys %v", k)
	}
	if k := Keys(t, doc, "data", "0", "row"); !slices.Equal(k, s.HitKeys) {
		t.Errorf("hit row keys %v, want v0.1.17's %v", k, s.HitKeys)
	}
	// human lines follow the hit order
	var lines strings.Builder
	for _, d := range env.Data {
		show := JSON(t, must(t, r, s.Noun+".show", `{"id":"`+d.ID+`"}`))
		title, body := "", Str(t, show, "data", "body")
		if s.TitleKey != "" {
			title = Str(t, show, "data", s.TitleKey)
		}
		if s.SearchLine != nil {
			lines.WriteString(s.SearchLine(d.ID, title, body))
		} else {
			lines.WriteString(d.ID + " " + title + "\n")
		}
	}
	if got := Text(res); got != lines.String() {
		t.Errorf("search text %q, want %q", got, lines.String())
	}
	// no hits: v0.1.17 printed "(no matches)" and "data": null (pinned for
	// every noun by ops/content TestEmptyEnvelopesMatchV0117).
	none := must(t, r, s.Noun+".search", `{"query":"zzzqqq"}`)
	if got := Text(none); got != "(no matches)\n" {
		t.Errorf("no hits text %q", got)
	}
	if got := Str(t, JSON(t, none), "kind"); got != s.KindSearch {
		t.Errorf("no hits kind %q", got)
	}
	// limit is applied after the filter
	one := JSON(t, must(t, r, s.Noun+".search", `{"query":"retry","limit":1}`))
	if !strings.Contains(one, `"count":1,`) {
		t.Errorf("limit 1: %s", one)
	}
	for name, in := range map[string]string{
		"empty query": `{"query":"  "}`,
		"limit -1":    `{"query":"retry","limit":-1}`,
		"limit 1001":  `{"query":"retry","limit":1001}`,
		"bad column":  `{"query":"retry","columns":["no_such_field"]}`,
	} {
		_, err := r.Run(s.Noun+".search", in)
		Fails(t, name, err, errs.InvalidInput)
	}
	checkColumns(t, s, r)
}

// checkColumns: --column <field> matches only that field, for every FTS
// field of the table (v0.1.17 named FTS5 columns by field name).
func checkColumns(t *testing.T, s Spec, r Runner) {
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	def, ok := reg.Table(s.Table)
	if !ok {
		t.Fatalf("no table %s", s.Table)
	}
	// "retry" is in the body of two rows and the title of one.
	for _, col := range def.FTS {
		res := must(t, r, s.Noun+".search", `{"query":"retry","columns":[`+Quote(col)+`]}`)
		var env struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(JSON(t, res)), &env); err != nil {
			t.Fatal(err)
		}
		for _, d := range env.Data {
			show := JSON(t, must(t, r, s.Noun+".show", `{"id":"`+d.ID+`"}`))
			if !strings.Contains(strings.ToLower(Str(t, show, "data", col)), "retry") {
				t.Errorf("--column %s: hit %s has no match in %s", col, d.ID, col)
			}
		}
		if col == "body" && len(env.Data) != 2 {
			t.Errorf("--column body: %d hits, want 2", len(env.Data))
		}
		if col == s.TitleKey && len(env.Data) != 1 {
			t.Errorf("--column %s: %d hits, want 1", col, len(env.Data))
		}
	}
}

func checkArchive(t *testing.T, s Spec, r Runner) {
	id := add(t, s, r, "Archived one", "archive retry body")
	res := must(t, r, s.Noun+".archive", `{"id":"`+id+`"}`)
	if got, want := Text(res), Mark+" "+id+" archived\n"; got != want {
		t.Errorf("archive text %q, want %q", got, want)
	}
	if !strings.Contains(JSON(t, must(t, r, s.Noun+".show", `{"id":"`+id+`"}`)), `"archived_at"`) {
		t.Error("show: no archived_at after archive")
	}
	// OV30-2: every list hides archived rows unless --archived (v0.1.17
	// hid them only on snapshot); search hides them too.
	if strings.Contains(Text(must(t, r, s.Noun+".list", "")), id) {
		t.Error("list shows an archived row without --archived (OV30-2)")
	}
	if !strings.Contains(Text(must(t, r, s.Noun+".list", `{"archived":true}`)), id) {
		t.Error("list --archived misses the archived row")
	}
	if strings.Contains(JSON(t, must(t, r, s.Noun+".search", `{"query":"retry"}`)), id) {
		t.Error("search shows an archived row")
	}
	if !strings.Contains(JSON(t, must(t, r, s.Noun+".search", `{"query":"retry","include_archived":true}`)), id) {
		t.Error("search --include-archived misses the archived row")
	}
	res = must(t, r, s.Noun+".unarchive", `{"id":"`+id+`"}`)
	if got, want := Text(res), Mark+" "+id+" unarchived\n"; got != want {
		t.Errorf("unarchive text %q, want %q", got, want)
	}
	if strings.Contains(JSON(t, must(t, r, s.Noun+".show", `{"id":"`+id+`"}`)), `"archived_at"`) {
		t.Error("show: archived_at kept after unarchive")
	}
	_, err := r.Run(s.Noun+".archive", `{"id":"`+fakeID(s.Prefix)+`"}`)
	Fails(t, "archive unknown", err, errs.NotFound)
}

func checkDelete(t *testing.T, s Spec, r Runner) {
	keep := add(t, s, r, "Keep", "kept body")
	id := add(t, s, r, "Gone", "gone retry body")
	_, err := r.Run(s.Noun+".delete", `{"id":"`+id+`"}`)
	Fails(t, "delete without --yes", err, errs.NeedsConfirm)
	if _, serr := r.Run(s.Noun+".show", `{"id":"`+id+`"}`); serr != nil {
		t.Fatalf("an unconfirmed delete removed the row: %v", serr)
	}
	res, err := r.RunYes(s.Noun+".delete", `{"id":"`+id+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Text(res), DeletedText(id); got != want {
		t.Errorf("delete text %q, want %q", got, want)
	}
	_, err = r.Run(s.Noun+".show", `{"id":"`+id+`"}`)
	Fails(t, "show deleted", err, errs.InTrash) // DELETION 3.2: says where it is
	_, err = r.RunYes(s.Noun+".delete", `{"id":"`+id+`"}`)
	Fails(t, "delete twice", err, errs.InTrash)
	list := Text(must(t, r, s.Noun+".list", ""))
	if strings.Contains(list, id) || !strings.Contains(list, keep) {
		t.Errorf("list after delete: %q", list)
	}
	if strings.Contains(JSON(t, must(t, r, s.Noun+".search", `{"query":"gone"}`)), id) {
		t.Error("search finds a deleted row")
	}
	if s.EditWord != "" {
		_, err = r.Run(s.Noun+".edit", s.EditTitle(id, "x"))
		Fails(t, "edit deleted", err, errs.InTrash)
	}
}

// AuditKeys are the writer keys every record carries after its v0.1.17
// keys (INTEG-AUDIT-1, ops/internal/auditrec).
var AuditKeys = []string{"created_by", "updated_by"}

// WriterLine is the writers line (INTEG-AUDIT-2, auditrec.Line) a plain
// show prints for the record at path in the --json document doc ("data"
// for a show): what every show text ends its main block with.
func WriterLine(t testing.TB, doc string, path ...string) string {
	t.Helper()
	at := func(k string) string { return Str(t, doc, append(slices.Clone(path), k)...) }
	return auditrec.Line(at("created_at"), at("created_by"), at("updated_at"), at("updated_by"))
}

// TitleLine is a Spec.ListLine for a noun whose list prints
// content.ListLine: the badge, the id and the record's key field (its
// title, name or body).
func TitleLine(badge, key string) func(t testing.TB, rec string) string {
	return func(t testing.TB, rec string) string {
		t.Helper()
		return content.ListLine(badge, Str(t, rec, "id"), Str(t, rec, key), Str(t, rec, "_label"))
	}
}
