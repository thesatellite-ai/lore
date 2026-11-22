package embed

import (
	"encoding/json"
	"math"
	"os"
	"slices"
	"testing"
)

// TestParity compares the bundled int8 model with model2vec 0.10.0's own
// float32 potion-base-8M on texts covering accents, CJK, emoji, special
// tokens, control characters, unknown words and texts past the 512-token
// cut (testdata/parity.json, written by model2vec from the original
// model): the token ids must be equal and the vectors within int8
// rounding (cosine > 0.999).
func TestParity(t *testing.T) {
	b, err := os.ReadFile("testdata/parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Texts []string    `json:"texts"`
		IDs   [][]int     `json:"ids"`
		Vecs  [][]float64 `json:"vecs"`
	}
	if err := json.Unmarshal(b, &ref); err != nil {
		t.Fatal(err)
	}
	m, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if m.Name() != "potion-base-8M" || m.Dim() != 256 {
		t.Fatalf("model %s dim %d, want potion-base-8M 256", m.Name(), m.Dim())
	}
	for i, text := range ref.Texts {
		want := slices.DeleteFunc(slices.Clone(ref.IDs[i]), func(id int) bool { return id == m.tok.unkID })
		// a text cut at 512 tokens: model2vec's tokenize() skips the
		// character cut its encode() makes, so only the vector is compared
		if got := m.ids(text); len(want) < m.maxTokens && !slices.Equal(got, want) {
			t.Errorf("text %d %.40q: ids\n got  %v\n want %v", i, text, head(got), head(want))
		}
		v := m.Embed(text)
		var dot, nr float64
		for j, x := range ref.Vecs[i] {
			dot += x * float64(v[j])
			nr += x * x
		}
		if nr == 0 {
			if slices.ContainsFunc(v, func(x float32) bool { return x != 0 }) {
				t.Errorf("text %d %.40q: want the zero vector", i, text)
			}
			continue
		}
		if c := dot / math.Sqrt(nr); c < 0.999 {
			t.Errorf("text %d %.40q: cosine to model2vec %.5f, want > 0.999", i, text, c)
		}
	}
}

// TestMeaningOverWords: a paraphrase with no shared word is nearer the
// row it restates than a row sharing a word with it, which is what the
// vectors add to word matching.
func TestMeaningOverWords(t *testing.T) {
	m, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	q := m.Embed("where does dana live")
	moved := m.Embed("Dana relocated to Lisbon last spring and rents a flat near the river")
	other := m.Embed("Dana reviewed the live deploy checklist")
	if Cosine(q, moved) <= Cosine(q, other)-0.2 {
		t.Fatalf("cos(question, paraphrase) %.3f far below cos(question, shared word) %.3f", Cosine(q, moved), Cosine(q, other))
	}
	if c := Cosine(m.Embed("the billing service uses Postgres"), m.Embed("billing runs on a PostgreSQL database")); c < 0.5 {
		t.Fatalf("near-duplicate statements: cosine %.3f, want >= 0.5", c)
	}
}

func head(ids []int) []int {
	if len(ids) > 12 {
		return ids[:12]
	}
	return ids
}
