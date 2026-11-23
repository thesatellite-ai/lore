package ask

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/embed"
	"github.com/thesatellite-ai/lore/model/value"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
)

// notesEnv answers an ask from canned memories (no resource, no task):
// the typed statement gets nothing, the text statement every note. vf
// counts what the call kept between processes.
type notesEnv struct {
	notes [][2]string // id, body
	keeps bool
	vf    *countingVectors
}

func (e *notesEnv) AskLens(_ context.Context, _ bool, fn func(q Query, keeps bool) error) error {
	return fn(func(_ context.Context, s string) (opssql.Result, error) {
		if strings.Contains(s, "resources_all") || !strings.Contains(s, "memories_all") {
			return opssql.Result{}, nil
		}
		txt := func(s string) value.Value { v, _ := value.TextOf(s); return v }
		var rows [][]value.Value
		for i, n := range e.notes {
			at := fmt.Sprintf("2026-11-%02dT00:00:00.000000000Z", i+1)
			rows = append(rows, []value.Value{txt("row"), txt("memories"), txt(n[0]), txt("me/life"), txt(""), txt(n[1]), txt(at), txt("0"), txt(""), txt(""), txt(" " + strings.ToLower(n[1]))})
		}
		return opssql.Result{Rows: rows}, nil
	}, e.keeps)
}

// Vectors and Keep make notesEnv a VectorFile when vf is set.
func (e *notesEnv) Vectors(model string, dim int) map[uint64][]float32 {
	if e.vf == nil {
		return nil
	}
	return e.vf.Vectors(model, dim)
}

func (e *notesEnv) Keep(model string, dim int, vecs map[uint64][]float32) {
	if e.vf != nil {
		e.vf.Keep(model, dim, vecs)
	}
}

// countingVectors is a VectorFile in memory that counts the vectors it
// did not hold yet (the rows a call had to embed).
type countingVectors struct {
	kept  map[uint64][]float32
	added int
}

func (c *countingVectors) Vectors(string, int) map[uint64][]float32 { return maps.Clone(c.kept) }

func (c *countingVectors) Keep(_ string, _ int, vecs map[uint64][]float32) {
	if c.kept == nil {
		c.kept = map[uint64][]float32{}
	}
	for k, v := range vecs {
		if _, ok := c.kept[k]; !ok {
			c.added++
		}
		c.kept[k] = v
	}
}

// TestMeaningRowJoins (devco D-902, hunt H-157): a question sharing no
// word with the note that answers it ("which physician do I see" → "My
// doctor is Dr Patel …") found nothing, because ask read only the rows
// holding a question word. The note nearest in meaning now joins, after
// the row holding a word ("see"), and a one-shot call answers as the
// read server does.
func TestMeaningRowJoins(t *testing.T) {
	t.Parallel()
	notes := [][2]string{
		{"mem_doc", "My doctor is Dr Patel at the Elm Street clinic."},
		{"mem_sync", "Moved the weekly sync to Thursdays."},
		{"mem_freeze", "Code freeze for the billing service starts Monday."},
		{"mem_lamp", "Bought a new desk lamp for the study."},
		{"mem_see", "See the budget notes before Friday."},
	}
	var texts []string
	for _, keeps := range []bool{true, false} {
		out, err := run(context.Background(), &notesEnv{notes: notes, keeps: keeps}, RunIn{Query: "which physician do I see"})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range out.Rows {
			ids = append(ids, r.ID)
		}
		if len(ids) != 2 || ids[0] != "mem_see" || ids[1] != "mem_doc" {
			t.Fatalf("keeps %v: rows %v, want [mem_see mem_doc] (the word's row, then the note nearest in meaning; no unrelated note)\n%s", keeps, ids, out.Text())
		}
		texts = append(texts, out.Text())
	}
	if texts[0] != texts[1] {
		t.Fatalf("served and one-shot answers differ:\n%s\n---\n%s", texts[0], texts[1])
	}
}

// TestMeaningLeavesNamedAnswersAlone: a question naming a resource is
// answered by the rows about it; a note near in meaning does not join.
func TestMeaningLeavesNamedAnswersAlone(t *testing.T) {
	t.Parallel()
	d := fixture(t)
	q := parseQuestion("what is the status of lumen", now)
	ms := matchResources(q, d.resources)
	if len(ms) == 0 {
		t.Fatal("fixture: lumen must match")
	}
	extra := textRow{Table: "memories", ID: "mem_near", Place: "me/life", Body: "Progress report of the project: on track.", At: now}
	c := corpusOfRows(append(append([]textRow{}, d.rows...), extra))
	if got := withMeaning(q, ms, d.rows, c, viewOf(q, c, nil), nil); len(got) != len(d.rows) {
		t.Fatalf("a named question got %d rows by meaning", len(got)-len(d.rows))
	}
}

// TestMeaningRowsDoNotPad: a row joins by meaning only when it is at
// least as near the question as the nearest row holding a word: a
// question its words answer is not padded with rows merely about the same
// things (the recall gate's answers grew by 60% when every free place
// was filled).
func TestMeaningRowsDoNotPad(t *testing.T) {
	t.Parallel()
	rows := []textRow{
		{Table: "memories", ID: "mem_park", Place: "me/life", Body: "Parking at the cabin: use the north lot, spot 12, gate opens with 4417.", At: now},
		{Table: "memories", ID: "mem_lot", Place: "me/life", Body: "The cabin garage fits two cars; leave the lane clear.", At: now},
	}
	c := corpusOfRows(rows)
	q := parseQuestion("what is the parking code", now)
	v := viewOf(q, c, nil)
	if v.cos[1] < meaningFloor || v.cos[1] >= v.cos[0] {
		t.Fatalf("fixture: the garage note must be near (%.3f) but less near than the parking note (%.3f)", v.cos[1], v.cos[0])
	}
	if got := withMeaning(q, nil, rows[:1], c, v, nil); len(got) != 1 {
		t.Fatalf("a question its words answer got %d rows by meaning", len(got)-1)
	}
}

// TestMeaningFloorSeparates: with the static vectors, a note answering a
// question in other words reaches meaningFloor and the unrelated notes do
// not, so a meaning row is an answer, not noise.
func TestMeaningFloorSeparates(t *testing.T) {
	t.Parallel()
	m, err := embed.Default()
	if err != nil {
		t.Fatal(err)
	}
	pairs := [][2]string{
		{"where did my sister relocate to", "Dana moved to Lisbon in March."},
		{"what car do I drive", "Bought a used Toyota Corolla, silver, 2019."},
		{"when is the dentist appointment", "Teeth cleaning booked for Tuesday 3pm."},
		{"which physician do I see", "My doctor is Dr Patel at the Elm Street clinic."},
		{"what pet do we have", "Our dog Biscuit is a beagle."},
		{"how much is the rent", "Monthly apartment payment is 1800 euros."},
		{"what did we choose for the database", "Picked Postgres over MySQL for the billing service."},
		{"what is the deploy procedure", "Release steps: tag, build, push the image, roll out."},
	}
	noise := []string{"Moved the weekly sync to Thursdays.", "Bought a new desk lamp for the study.", "Code freeze for the billing service starts Monday.", "Wrote code all afternoon, then a long walk.", "Lunch order: two salads and a soup.", "Invoice numbering restarts every January."}
	vec := func(s string) []float32 { return m.Embed(docText(&textRow{Body: s})) }
	for _, p := range pairs {
		q := m.Embed(p[0])
		if c := embed.Cosine(q, vec(p[1])); c < meaningFloor {
			t.Errorf("%q ~ %q: cosine %.3f below the floor %.2f", p[0], p[1], c, meaningFloor)
		}
		for _, n := range noise {
			if c := embed.Cosine(q, vec(n)); c >= meaningFloor {
				t.Errorf("%q ~ unrelated %q: cosine %.3f reaches the floor %.2f", p[0], n, c, meaningFloor)
			}
		}
	}
}

// TestVectorsKeptBetweenProcesses: a call embeds the rows it has not seen
// and hands them to the VectorFile; a new process with the same file
// embeds none (a one-shot call does not pay ~80 µs per row each time),
// and a changed row is the only one embedded again.
func TestVectorsKeptBetweenProcesses(t *testing.T) {
	notes := [][2]string{
		{"mem_k1", "Kept vector one: the harbour ferry leaves at nine."},
		{"mem_k2", "Kept vector two: the bakery closes on Sundays."},
		{"mem_k3", "Kept vector three: the ferry ticket costs four euros."},
	}
	vf := &countingVectors{}
	newProcess := func() {
		vecMu.Lock()
		clear(vecKept)
		vecLoaded = false
		vecMu.Unlock()
		corpusMu.Lock()
		clear(corpusKept)
		corpusMu.Unlock()
		termMu.Lock()
		clear(termKept)
		termMu.Unlock()
	}
	ask := func() {
		if _, err := run(context.Background(), &notesEnv{notes: notes, vf: vf}, RunIn{Query: "when does the ferry leave"}); err != nil {
			t.Fatal(err)
		}
	}
	newProcess()
	ask()
	if vf.added != len(notes) {
		t.Fatalf("first call kept %d vectors, want %d", vf.added, len(notes))
	}
	newProcess()
	ask()
	if vf.added != len(notes) {
		t.Fatalf("a new process embedded %d rows the file held", vf.added-len(notes))
	}
	notes[1][1] = "Kept vector two: the bakery now opens on Sundays."
	newProcess()
	ask()
	if vf.added != len(notes)+1 {
		t.Fatalf("after one row changed %d vectors were kept, want 1", vf.added-len(notes))
	}
}

// TestCorpusVectorsSurviveTheBound: past maxKeptVectors the process
// keeps the vectors of the corpus just indexed, not an empty cache (a
// store bigger than the bound would else embed every row on every call).
func TestCorpusVectorsSurviveTheBound(t *testing.T) {
	big := map[uint64][]float32{}
	for i := range maxKeptVectors + 5 {
		big[uint64(i)+1<<50] = []float32{1}
	}
	keepCorpusVectors(big)
	for _, k := range []uint64{1 << 50, 1<<50 + maxKeptVectors + 4} {
		if _, ok := keptVector(k); !ok {
			t.Fatalf("vector %d of the corpus in use was dropped", k)
		}
	}
	vecMu.Lock()
	clear(vecKept)
	vecMu.Unlock()
}

// TestKeptCorporaBoundedAndCounted: the corpus of the same cells is
// parsed and indexed once, at most maxKeptCorpora are kept, the least
// recently used going first, and what is kept counts in KeptBytes (the
// read server's limits).
func TestKeptCorporaBoundedAndCounted(t *testing.T) {
	txt := func(s string) value.Value { v, _ := value.TextOf(s); return v }
	lines := func(n int) [][]value.Value {
		body := fmt.Sprintf("corpus %d: the ferry timetable", n)
		return [][]value.Value{{txt("row"), txt("memories"), txt(fmt.Sprintf("mem_c%d", n)), txt("me/life"), txt(""), txt(body), txt("2026-11-01T00:00:00.000000000Z"), txt("0"), txt(""), txt(""), txt(body), txt("2026-11-01T00:00:00Z"), txt("2026-11-01T00:00:00Z")}}
	}
	first := corpusOf(lines(0))
	ci := first.index(nil)
	for n := 1; n < maxKeptCorpora+3; n++ {
		corpusOf(lines(n))
		if c := corpusOf(lines(0)); c != first || c.index(nil) != ci {
			t.Fatalf("after %d more corpora the one in use was parsed or indexed again", n)
		}
	}
	corpusMu.Lock()
	kept := len(corpusKept)
	corpusMu.Unlock()
	if kept > maxKeptCorpora {
		t.Fatalf("%d corpora kept, bound %d", kept, maxKeptCorpora)
	}
	if KeptBytes() <= 0 {
		t.Fatal("kept corpora are not counted in KeptBytes")
	}
}
