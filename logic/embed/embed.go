// Package embed turns text into a meaning vector with no model runtime
// (layer L1, pure): a model2vec static embedding model, potion-base-8M
// stored as int8 (MIT licensed, see NOTICE), shipped inside the binary.
// A text's vector is the mean of its WordPiece tokens' table rows,
// L2-normalised, so the cosine of two vectors is their dot product.
//
// Why (devco D-894, hunt H-149): lore ask found rows only by the words
// they share with the question; a row that says the same thing in other
// words ("moved to Lisbon" for "where does she live") was never shown.
// Static vectors close most of that gap at microseconds per text: on
// LongMemEval_S turn recall_any@5 the fused lexical + vector order scored
// 0.92 against 0.81 for word counting alone.
//
// Default loads the bundled model once per process (~10 ms, a few MB of
// decoded vocabulary); Embed is safe for concurrent use.
package embed

import (
	_ "embed" // the bundled model
	"math"
	"sync"
)

//go:embed potion-base-8M-i8.lse
var bundled []byte

// Model is a static embedding model: a tokenizer and an int8 table.
type Model struct {
	name      string
	tok       *tokenizer
	dim       int
	table     []byte    // int8 rows*dim, read in place
	scales    []float32 // one scale per table row
	normalize bool
	maxTokens int // token ids kept (model2vec's max_length)
	maxChars  int // runes of text kept before tokenising
}

var (
	defaultOnce  sync.Once
	defaultModel *Model
	defaultErr   error
)

// Default is the bundled potion-base-8M model, loaded on first use.
func Default() (*Model, error) {
	defaultOnce.Do(func() { defaultModel, defaultErr = parseBundle(bundled) })
	return defaultModel, defaultErr
}

// Name is the model's name (potion-base-8M).
func (m *Model) Name() string { return m.name }

// Dim is the vector width.
func (m *Model) Dim() int { return m.dim }

// ids tokenises text the way model2vec does: char truncation, ids, token
// truncation, unknown ids dropped.
func (m *Model) ids(text string) []int {
	if m.maxChars > 0 && len(text) > m.maxChars {
		if r := []rune(text); len(r) > m.maxChars {
			text = string(r[:m.maxChars])
		}
	}
	ids := m.tok.encode(text)
	if m.maxTokens > 0 && len(ids) > m.maxTokens {
		ids = ids[:m.maxTokens]
	}
	out := ids[:0]
	for _, id := range ids {
		if id != m.tok.unkID {
			out = append(out, id)
		}
	}
	return out
}

// Embed returns text's vector (len Dim): the mean of its tokens' rows,
// L2-normalised; the zero vector when no token is known.
func (m *Model) Embed(text string) []float32 {
	out := make([]float32, m.dim)
	ids := m.ids(text)
	if len(ids) == 0 {
		return out
	}
	d := m.dim
	for _, id := range ids {
		row := m.table[id*d : id*d+d]
		s := m.scales[id]
		for i, v := range row {
			out[i] += s * float32(int8(v))
		}
	}
	inv := 1 / float32(len(ids))
	for i := range out {
		out[i] *= inv
	}
	if m.normalize {
		var sum float64
		for _, x := range out {
			sum += float64(x) * float64(x)
		}
		n := float32(math.Sqrt(sum) + 1e-32) // model2vec adds 1e-32
		for i := range out {
			out[i] /= n
		}
	}
	return out
}

// Cosine is the cosine of two normalised vectors (their dot product).
func Cosine(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}
