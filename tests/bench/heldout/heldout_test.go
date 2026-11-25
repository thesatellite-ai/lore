package heldout

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGenerateDeterministic: the same seed gives the same store byte for
// byte (the question files are written against it), another seed another.
func TestGenerateDeterministic(t *testing.T) {
	a, err := Generate(DefaultSeed).Batch("")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Generate(DefaultSeed).Batch("")
	if !bytes.Equal(a, b) {
		t.Fatal("two stores of the same seed differ")
	}
	c, _ := Generate(DefaultSeed + 1).Batch("")
	if bytes.Equal(a, c) {
		t.Fatal("another seed gave the same store")
	}
}

// TestGenerateShape: the store has the real shape of the benchmark store
// (owner set 7) and every shape of sets 5-7.
func TestGenerateShape(t *testing.T) {
	s := Generate(DefaultSeed)
	var notes, short, other int
	keys := map[string]bool{}
	for _, r := range s.Rows {
		if keys[r.Key] {
			t.Errorf("bench_key %s used twice", r.Key)
		}
		keys[r.Key] = true
		var names []string
		for _, m := range r.Meta {
			names = append(names, m.Name)
		}
		if strings.Join(names[:4], ",") != strings.Join([]string{MetaKey, MetaTopic, MetaState, MetaVerified}, ",") || r.Meta[0].Value != r.Key {
			t.Errorf("%s: meta %v, want bench_key, fact_topic, fact_state, fact_verified_at first", r.Key, names)
		}
		switch {
		case r.Folder == OtherFolder:
			other++
		case r.Kind == KindNote:
			notes++
			if n := len(r.Body); n < 5000 || n > 15000 {
				t.Errorf("%s: long note of %d bytes, want 5-15 kB", r.Key, n)
			}
			if n := strings.Count(r.Body, "\n## ") + strings.Count(r.Body, "\n### "); n < 12 {
				t.Errorf("%s: %d sections, want many (>= 12)", r.Key, n)
			}
		default:
			short++
		}
	}
	if notes < 10 || notes > 12 {
		t.Errorf("%d long notes in %s, want 10-12", notes, AppFolder)
	}
	if short < 140 || short > 170 {
		t.Errorf("%d short rows in %s, want ~150", short, AppFolder)
	}
	if other < 20 {
		t.Errorf("%d rows in %s, want >= 20", other, OtherFolder)
	}
	if len(s.Cards) < 18 || len(s.Cards) > 22 {
		t.Errorf("%d code_repo cards, want ~20", len(s.Cards))
	}
	// cards share common words with the notes
	notesText := ""
	for _, r := range s.Rows {
		if r.Kind == KindNote {
			notesText += strings.ToLower(r.Body)
		}
	}
	sharing := 0
	for _, c := range s.Cards {
		for w := range strings.SplitSeq(c.Name, "_") {
			if strings.Contains(notesText, " "+w) {
				sharing++
				break
			}
		}
	}
	if sharing < len(s.Cards)*3/4 {
		t.Errorf("only %d of %d card names share a word with the notes", sharing, len(s.Cards))
	}
	// the shapes
	if len(s.Shapes.Conflicts) < 10 {
		t.Errorf("%d contradicting pairs, want one per component", len(s.Shapes.Conflicts))
	}
	for _, p := range s.Shapes.Conflicts {
		a, _ := s.Row(p[0])
		b, _ := s.Row(p[1])
		if !strings.Contains(a.Body, "defaults to") || !strings.Contains(b.Body, "has no default") {
			t.Errorf("conflict %v: want a value and a 'no default' row:\n%s\n%s", p, a.Body, b.Body)
		}
	}
	for _, k := range s.Shapes.Deep {
		r, _ := s.Row(k)
		head := r.Body[:strings.Index(r.Body, "## Deploy-time")]
		if float64(len(head)) < 0.55*float64(len(r.Body)) {
			t.Errorf("%s: the subject section starts at %d of %d bytes, want in the last part", k, len(head), len(r.Body))
		}
	}
	for _, k := range s.Shapes.Passing {
		r, _ := s.Row(k)
		cmd := r.Body[1 : strings.Index(r.Body[1:], "`")+1]
		if !strings.Contains(notesText, strings.ToLower(cmd)) {
			t.Errorf("%s: its command %q is in no long note", k, cmd)
		}
	}
	if len(s.Shapes.Siblings) < 3 || len(s.Shapes.Exact) < 60 {
		t.Errorf("siblings %d, exact rows %d", len(s.Shapes.Siblings), len(s.Shapes.Exact))
	}
	for _, k := range append(append([]string{}, s.Shapes.Deep...), s.Shapes.Exact...) {
		if !keys[k] {
			t.Errorf("shape names %s, which is no row", k)
		}
	}
}

// TestBatchAndDump: the batch file writes every row with its meta, and the
// dump has one file per row and card holding its key and text.
func TestBatchAndDump(t *testing.T) {
	s := Generate(DefaultSeed)
	raw, err := s.Batch("")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != len(s.Rows) {
		t.Fatalf("%d batch lines for %d rows", len(lines), len(s.Rows))
	}
	for i, l := range lines {
		var bl batchLine
		if err := json.Unmarshal([]byte(l), &bl); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		if bl.Input.Meta[0] != MetaKey+"="+s.Rows[i].Key || bl.Op != string(s.Rows[i].Kind)+" add" {
			t.Errorf("line %d: op %q meta %v", i+1, bl.Op, bl.Input.Meta)
		}
	}
	dir := t.TempDir()
	if err := s.WriteDump(dir); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	if len(files) != len(s.Rows)+len(s.Cards)+1 {
		t.Errorf("%d dump files, want %d", len(files), len(s.Rows)+len(s.Cards)+1)
	}
	r := s.Rows[0]
	got, err := os.ReadFile(filepath.Join(dir, r.Key+".md"))
	if err != nil || !strings.HasPrefix(string(got), "bench_key: "+r.Key+"\n") || !strings.Contains(string(got), r.Body) {
		t.Errorf("dump of %s: %v\n%.300s", r.Key, err, got)
	}
}

// TestLoadQuestions: a good file loads; every kind of bad line is named
// with its line number; an empty file is zero questions.
func TestLoadQuestions(t *testing.T) {
	s := Generate(DefaultSeed)
	conflict := s.Shapes.Conflicts[0][1]
	r, _ := s.Row(conflict)
	sentence := strings.SplitAfter(r.Body, ".")[0]
	q := func(id, key, sent, kind string) string {
		raw, _ := json.Marshal(Question{ID: id, Question: "what is it", AnswerKey: key, AnswerSentence: sent, Kind: QuestionKind(kind)})
		return string(raw)
	}
	good := q("Q-1", conflict, sentence, "conflict") + "\n\n" + q("Q-2", "card-webhook_relay", "Relays incoming webhook calls", "resource") + "\n"
	qs, err := LoadQuestions(strings.NewReader(good), s)
	if err != nil || len(qs) != 2 {
		t.Fatalf("good file: %v %v", qs, err)
	}
	if qs, err := LoadQuestions(strings.NewReader(""), s); err != nil || len(qs) != 0 {
		t.Fatalf("empty file: %v %v", qs, err)
	}
	bad := strings.Join([]string{
		q("Q-1", conflict, sentence, "conflict"),
		q("Q-1", conflict, sentence, "conflict"),
		q("Q-3", "app-nothing-m01", "x", "exact"),
		q("Q-4", conflict, "a sentence the row does not hold", "exact"),
		q("Q-5", conflict, sentence, "trivia"),
		`{"id": "Q-6", "question": "q", "answer_key": "k", "answer_sentence": "s", "kind": "exact", "extra": 1}`,
		`not json`,
	}, "\n")
	_, err = LoadQuestions(strings.NewReader(bad), s)
	if !errors.Is(err, ErrQuestions) {
		t.Fatalf("bad file: %v", err)
	}
	for _, want := range []string{"line 2 (Q-1): id used twice", "line 3 (Q-3): answer_key", "line 4 (Q-4): answer_sentence is not in", "line 5 (Q-5): kind \"trivia\"", "line 6:", "line 7:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say %q:\n%v", want, err)
		}
	}
}

// answerJSON builds an ask --json answer of rows (id, body) and cards.
func answerJSON(t *testing.T, rows [][2]string, cards ...string) Answer {
	t.Helper()
	type row struct {
		ID    string `json:"id"`
		Table string `json:"table"`
		Body  string `json:"body"`
	}
	type field struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	type res struct {
		ID     string  `json:"id"`
		Kind   string  `json:"kind"`
		Name   string  `json:"name"`
		Fields []field `json:"fields"`
	}
	type item struct{ Kind, ID string }
	var out struct {
		Rows      []row  `json:"rows"`
		Resources []res  `json:"resources"`
		Ranked    []item `json:"ranked"`
	}
	out.Rows, out.Resources = []row{}, []res{}
	for _, r := range rows {
		out.Rows = append(out.Rows, row{r[0], "memories", r[1]})
		out.Ranked = append(out.Ranked, item{"row", r[0]})
	}
	for i, c := range cards {
		id := "res_" + string(rune('a'+i))
		out.Resources = append(out.Resources, res{id, "code_repo", c, []field{{"purpose", "Relays incoming webhook calls to the worker queue"}}})
		out.Ranked = append(out.Ranked, item{"resource", id})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(bytes.ReplaceAll(raw, []byte(`"Kind"`), []byte(`"kind"`)), []byte(`"ID"`), []byte(`"id"`))
	a, err := ParseAnswer(raw)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestScore: enough, shown-but-cut, not shown, first, cards when rows fill
// the slots, and a card answer.
func TestScore(t *testing.T) {
	s := Generate(DefaultSeed)
	keyOf := map[string]string{"mem_1": "app-keymint-m01", "mem_2": "app-keymint-m02", "mem_3": "app-quillq-m01", "mem_4": "app-quillq-m02"}
	q := Question{ID: "Q", AnswerKey: "app-keymint-m02", AnswerSentence: "Exits Non-Zero", Kind: QExact}
	cases := []struct {
		name                       string
		a                          Answer
		enough, shown, top1, cards bool
		rank                       int
	}{
		{"enough at 2", answerJSON(t, [][2]string{{"mem_1", "x"}, {"mem_2", "it   exits non-zero when"}}), true, true, false, false, 2},
		{"first, cut", answerJSON(t, [][2]string{{"mem_2", "it prints … (cut)"}}), false, true, true, false, 1},
		{"not shown", answerJSON(t, [][2]string{{"mem_3", "exits non-zero"}}), false, false, false, false, 0},
		{"card under full rows", answerJSON(t, [][2]string{{"mem_1", "a"}, {"mem_3", "b"}, {"mem_4", "c"}, {"mem_2", "exits non-zero"}}, "webhook_relay"), true, true, false, true, 4},
		{"card after two rows is not counted", answerJSON(t, [][2]string{{"mem_1", "a"}, {"mem_2", "exits non-zero"}}, "webhook_relay"), true, true, false, false, 2},
	}
	for _, c := range cases {
		r := Score(q, c.a, keyOf, s)
		if r.Enough != c.enough || r.Shown != c.shown || r.Top1 != c.top1 || r.CardsWhenFull != c.cards || r.Rank != c.rank {
			t.Errorf("%s: got enough %v shown %v top1 %v cards %v rank %d (shown %v)", c.name, r.Enough, r.Shown, r.Top1, r.CardsWhenFull, r.Rank, r.Shown3)
		}
	}
	card := Question{ID: "C", AnswerKey: "card-webhook_relay", AnswerSentence: "relays incoming webhook calls", Kind: QResource}
	if r := Score(card, answerJSON(t, [][2]string{{"mem_1", "a"}}, "webhook_relay"), keyOf, s); !r.Enough || r.Rank != 2 {
		t.Errorf("card answer: %+v", r)
	}
}

// TestReport: the summary line and the miss list.
func TestReport(t *testing.T) {
	rs := []Result{
		{Q: Question{ID: "Q-1", Question: "a", AnswerKey: "k1", Kind: QExact}, Enough: true, Shown: true, Top1: true, Rank: 1, Warm: 20 * time.Millisecond},
		{Q: Question{ID: "Q-2", Question: "b", AnswerKey: "k2", Kind: QSubject}, Shown: true, Rank: 3, Warm: 40 * time.Millisecond, Shown3: []string{"x (row)"}},
		{Q: Question{ID: "Q-3", Question: "c", AnswerKey: "k3", Kind: QSubject}, CardsWhenFull: true, Warm: 30 * time.Millisecond},
	}
	out := Report("tune.jsonl", DefaultSeed, rs)
	for _, want := range []string{
		"ask alone enough 1/3, answer shown 2/3, answer first 1/3, cards when rows fill the slots 1, warm max 40 ms (median 30 ms)",
		"| subject | 2 | 0 | 1 | 0 |",
		`- Q-2 (subject) "b": shown at 3 but the answer sentence is not in what ask printed for it; want k2`,
		`- Q-3 (subject) "c": the answer is not shown`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(Report("empty.jsonl", DefaultSeed, nil), "ask alone enough 0/0") {
		t.Error("empty report")
	}
}
