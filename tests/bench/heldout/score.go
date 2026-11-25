package heldout

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"
)

// QuestionKind is what a question asks for (see _FORMAT.md of the dump).
type QuestionKind string

// The question kinds a writer may use.
const (
	QExact    QuestionKind = "exact"
	QDefault  QuestionKind = "default"
	QSubject  QuestionKind = "subject"
	QWhy      QuestionKind = "why"
	QConflict QuestionKind = "conflict"
	QSibling  QuestionKind = "sibling"
	QResource QuestionKind = "resource"
	QHowTo    QuestionKind = "howto"
)

var questionKinds = []QuestionKind{QExact, QDefault, QSubject, QWhy, QConflict, QSibling, QResource, QHowTo}

// Question is one line of a question file.
type Question struct {
	ID             string       `json:"id"`
	Question       string       `json:"question"`
	AnswerKey      string       `json:"answer_key"`
	AnswerSentence string       `json:"answer_sentence"`
	Kind           QuestionKind `json:"kind"`
}

// ErrQuestions is returned when a question file has bad lines; the message
// lists every bad line.
var ErrQuestions = errors.New("bad question file")

// LoadQuestions reads a JSON-lines question file and checks every line
// against the store: the fields are set, ids are unique, the kind is known,
// answer_key names a row or card, and answer_sentence is an exact part of
// that row's title and body (or the card's purpose), so a miss is lore's,
// never the question file's. Blank lines are skipped; an empty file is zero
// questions.
func LoadQuestions(r io.Reader, s *Store) ([]Question, error) {
	var (
		qs   []Question
		bad  []string
		seen = map[string]bool{}
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var q Question
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&q); err != nil {
			bad = append(bad, fmt.Sprintf("line %d: %v", n, err))
			continue
		}
		if msg := checkQuestion(q, s, seen); msg != "" {
			bad = append(bad, fmt.Sprintf("line %d (%s): %s", n, q.ID, msg))
			continue
		}
		seen[q.ID] = true
		qs = append(qs, q)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("load questions: %w", err)
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("%w:\n  %s", ErrQuestions, strings.Join(bad, "\n  "))
	}
	return qs, nil
}

// LoadQuestionFile is LoadQuestions over a file.
func LoadQuestionFile(path string, s *Store) ([]Question, error) {
	f, err := os.Open(path) // #nosec G304 -- the bench's own question file
	if err != nil {
		return nil, fmt.Errorf("load questions: %w", err)
	}
	defer func() { _ = f.Close() }()
	return LoadQuestions(f, s)
}

func checkQuestion(q Question, s *Store, seen map[string]bool) string {
	switch {
	case q.ID == "" || q.Question == "" || q.AnswerKey == "" || q.AnswerSentence == "":
		return "id, question, answer_key and answer_sentence are required"
	case seen[q.ID]:
		return "id used twice"
	case !slices.Contains(questionKinds, q.Kind):
		return fmt.Sprintf("kind %q is not one of %v", q.Kind, questionKinds)
	}
	text, ok := s.answerText(q.AnswerKey)
	if !ok {
		return fmt.Sprintf("answer_key %q names no row or card of the store (seed %d)", q.AnswerKey, s.Seed)
	}
	if !contains(text, q.AnswerSentence) {
		return fmt.Sprintf("answer_sentence is not in %s", q.AnswerKey)
	}
	return ""
}

// answerText is what holds a key's answer: a row's title and body, a
// card's purpose.
func (s *Store) answerText(key string) (string, bool) {
	if r, ok := s.Row(key); ok {
		return r.Text(), true
	}
	if c, ok := s.Card(key); ok {
		return c.Purpose, true
	}
	return "", false
}

// contains is a substring test that ignores case and runs of white space,
// so a sentence copied from the dump matches the same words wrapped or
// spaced differently in lore's output.
func contains(text, sentence string) bool {
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	return strings.Contains(norm(text), norm(sentence))
}

// Answer is the part of lore ask --json the scorer reads.
type Answer struct {
	Rows []struct {
		ID    string `json:"id"`
		Table string `json:"table"`
		Title string `json:"title"`
		Body  string `json:"body"`
	} `json:"rows"`
	Resources []struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Fields []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"fields"`
	} `json:"resources"`
	Ranked []struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"ranked"`
	// Candidates are the one-line items after the full ones (H-245): an
	// answer there counts as shown, after every full item, and as enough
	// when its line holds the answer sentence.
	Candidates []struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Title string `json:"title"`
		Line  string `json:"line"`
	} `json:"candidates"`
}

// ParseAnswer reads lore ask --json output.
func ParseAnswer(raw []byte) (Answer, error) {
	var a Answer
	if err := json.Unmarshal(raw, &a); err != nil {
		return Answer{}, fmt.Errorf("parse ask --json: %w", err)
	}
	return a, nil
}

// Slots is how many items the benchmark's reader looks at first.
const Slots = 3

// Result is one question's score.
type Result struct {
	Q Question
	// Enough: the answer is shown and its sentence is in what ask printed
	// for it ("ask alone enough").
	Enough bool
	Shown  bool // the answering row or card is in the answer at all
	Top1   bool // it is the first item of the answer
	Rank   int  // its place in the answer, 1-based (a candidate line after every full item); 0 when not shown
	// CardsWhenFull: ask printed a code_repo card although knowledge rows
	// filled the first Slots places (and the answer is not a card).
	CardsWhenFull bool
	Warm          time.Duration
	Shown3        []string // the first Slots items, as "<bench_key> (<table>)"
}

// Score scores one answer. keyOf maps a row id of this run to its
// bench_key; cards are matched by name.
func Score(q Question, a Answer, keyOf map[string]string, s *Store) Result {
	res := Result{Q: q}
	keyOfItem := func(kind, id string) string {
		if kind == "resource" {
			for _, r := range a.Resources {
				if r.ID == id {
					if _, ok := s.Card("card-" + r.Name); ok {
						return "card-" + r.Name
					}
					return "resource:" + r.Name
				}
			}
		}
		if k, ok := keyOf[id]; ok {
			return k
		}
		return id
	}
	rowsFirst := 0
	for i, it := range a.Ranked {
		k := keyOfItem(it.Kind, it.ID)
		if i < Slots {
			res.Shown3 = append(res.Shown3, fmt.Sprintf("%s (%s)", k, it.Kind))
			if it.Kind == "row" && i == rowsFirst {
				rowsFirst++
			}
		}
		if k == q.AnswerKey && res.Rank == 0 {
			res.Rank, res.Shown = i+1, true
			res.Enough = contains(printed(a, it.Kind, it.ID), q.AnswerSentence)
		}
	}
	for i, c := range a.Candidates {
		if res.Rank != 0 {
			break
		}
		k := keyOf[c.ID]
		if c.Kind == "resource" {
			k = "resource:" + c.Title
			if _, ok := s.Card("card-" + c.Title); ok {
				k = "card-" + c.Title
			}
		}
		if k == q.AnswerKey {
			res.Rank, res.Shown = len(a.Ranked)+i+1, true
			res.Enough = contains(c.Title+"\n"+c.Line, q.AnswerSentence)
		}
	}
	res.Top1 = res.Rank == 1
	if rowsFirst >= Slots && q.Kind != QResource {
		for _, r := range a.Resources {
			if r.Kind == "code_repo" {
				res.CardsWhenFull = true
			}
		}
	}
	return res
}

// printed is the text ask printed for one item of its answer.
func printed(a Answer, kind, id string) string {
	if kind == "resource" {
		for _, r := range a.Resources {
			if r.ID == id {
				var b strings.Builder
				for _, f := range r.Fields {
					b.WriteString(f.Name + " " + f.Value + "\n")
				}
				return b.String()
			}
		}
		return ""
	}
	for _, r := range a.Rows {
		if r.ID == id {
			return r.Title + "\n" + r.Body
		}
	}
	return ""
}

// Summary is the totals of a run.
type Summary struct {
	N, Enough, Shown, Top1, CardsWhenFull int
	WarmMax, WarmMedian                   time.Duration
}

// Summarize totals the results.
func Summarize(rs []Result) Summary {
	var s Summary
	var warm []time.Duration
	for _, r := range rs {
		s.N++
		s.Enough += b2i(r.Enough)
		s.Shown += b2i(r.Shown)
		s.Top1 += b2i(r.Top1)
		s.CardsWhenFull += b2i(r.CardsWhenFull)
		warm = append(warm, r.Warm)
	}
	if len(warm) > 0 {
		slices.Sort(warm)
		s.WarmMax, s.WarmMedian = warm[len(warm)-1], warm[len(warm)/2]
	}
	return s
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Line is the one-line score the bench prints and the status files quote.
func (s Summary) Line() string {
	return fmt.Sprintf("held-out: ask alone enough %d/%d, answer shown %d/%d, answer first %d/%d, cards when rows fill the slots %d, warm max %d ms (median %d ms)",
		s.Enough, s.N, s.Shown, s.N, s.Top1, s.N, s.CardsWhenFull, s.WarmMax.Milliseconds(), s.WarmMedian.Milliseconds())
}

// Report is the markdown report: the score, the score per kind, and every
// question that was not "ask alone enough" with what ask showed instead.
func Report(file string, seed uint64, rs []Result) string {
	var b strings.Builder
	sum := Summarize(rs)
	fmt.Fprintf(&b, "# Held-out evaluation of lore ask\n\nQuestions: `%s` (%d), store seed %d.\n\n%s\n\n", file, sum.N, seed, sum.Line())
	b.WriteString("| kind | questions | alone enough | shown | first |\n|---|---|---|---|---|\n")
	for _, k := range questionKinds {
		var of []Result
		for _, r := range rs {
			if r.Q.Kind == k {
				of = append(of, r)
			}
		}
		if len(of) == 0 {
			continue
		}
		ks := Summarize(of)
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n", k, ks.N, ks.Enough, ks.Shown, ks.Top1)
	}
	b.WriteString("\n## Not enough alone\n\n")
	misses := 0
	for _, r := range rs {
		if r.Enough {
			continue
		}
		misses++
		why := "the answer is not shown"
		if r.Shown {
			why = fmt.Sprintf("shown at %d but the answer sentence is not in what ask printed for it", r.Rank)
		}
		fmt.Fprintf(&b, "- %s (%s) %q: %s; want %s; first %d shown: %s\n", r.Q.ID, r.Q.Kind, r.Q.Question, why, r.Q.AnswerKey, Slots, strings.Join(r.Shown3, ", "))
	}
	if misses == 0 {
		b.WriteString("none\n")
	}
	return b.String()
}
