package embed

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// A bundle is one file holding a WordPiece model2vec model ready to use:
// settings, the vocabulary as ids sorted by token bytes over a token blob
// (binary search, no map built at load), and an int8 table with one
// float32 scale per row.
//
//	"LSE1" | u32 header length | header JSON
//	u32[n] ids sorted by token bytes | u32[n+1] token offsets by id | token blob
//	pad | int8[rows*dim] table | f32[rows] scales
//
// Everything is read little-endian with encoding/binary (no unsafe views),
// so the embedded bytes need no alignment and any GOARCH reads them alike.
const bundleMagic = "LSE1"

// bundleHeader is the bundle's JSON header (the field names the writer
// used, offsets in bytes from the start of the file).
type bundleHeader struct {
	Kind      string  `json:"kind"`
	Prefix    string  `json:"prefix"`
	MaxChars  int     `json:"max_chars_per_word"`
	UnkID     int     `json:"unk_id"`
	Bert      [4]bool `json:"bert"` // clean, chinese, strip accents, lower
	Added     []added `json:"added"`
	Median    int     `json:"median"`
	Normalize bool    `json:"normalize"`
	MaxTokens int     `json:"max_tokens"`
	N         int     `json:"n"`
	Rows      int     `json:"rows"`
	Dim       int     `json:"dim"`
	DType     string  `json:"dtype"`
	Name      string  `json:"name"`
	OrderAt   int
	OffAt     int
	BlobAt    int
	RemapAt   int
	TableAt   int
	ScalesAt  int
}

// added is a special token matched on the raw text before splitting
// ([CLS], [SEP] …), as the Hugging Face tokenizer does.
type added struct {
	ID         int    `json:"id"`
	Content    string `json:"content"`
	Normalized bool   `json:"normalized"`
}

// parseBundle reads a bundle image. b must stay unchanged while the model
// is used (the table is read in place).
func parseBundle(b []byte) (*Model, error) {
	if len(b) < 8 || string(b[:4]) != bundleMagic {
		return nil, errs.New(errs.Internal, "embed: the model bundle has no LSE1 header")
	}
	hl := int(binary.LittleEndian.Uint32(b[4:8]))
	if 8+hl > len(b) {
		return nil, errs.New(errs.Internal, "embed: the model bundle header is cut short")
	}
	var h bundleHeader
	if err := json.Unmarshal(b[8:8+hl], &h); err != nil {
		return nil, errs.Wrap(errs.Internal, "embed: read the model bundle header", err)
	}
	if h.Kind != "WordPiece" || h.DType != "I8" || h.RemapAt >= 0 || h.Rows != h.N {
		return nil, errs.New(errs.Internal, "embed: only an unpruned WordPiece int8 bundle is supported")
	}
	n := h.N
	if h.OrderAt+4*n > len(b) || h.OffAt+4*(n+1) > len(b) || h.TableAt+h.Rows*h.Dim > len(b) || h.ScalesAt+4*h.Rows > len(b) {
		return nil, errs.New(errs.Internal, "embed: the model bundle is cut short")
	}
	u32s := func(at, cnt int) []uint32 {
		out := make([]uint32, cnt)
		for i := range out {
			out[i] = binary.LittleEndian.Uint32(b[at+4*i:])
		}
		return out
	}
	t := &tokenizer{
		order: u32s(h.OrderAt, n), off: u32s(h.OffAt, n+1), blob: string(b[h.BlobAt:h.TableAt]),
		prefix: h.Prefix, maxChars: h.MaxChars, unkID: h.UnkID,
		clean: h.Bert[0], chinese: h.Bert[1], strip: h.Bert[2], lower: h.Bert[3],
	}
	for _, a := range h.Added {
		if !a.Normalized && a.Content != "" {
			t.added = append(t.added, a)
		}
	}
	scales := make([]float32, h.Rows)
	for i := range scales {
		scales[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[h.ScalesAt+4*i:]))
	}
	m := &Model{name: h.Name, tok: t, dim: h.Dim, table: b[h.TableAt : h.TableAt+h.Rows*h.Dim], scales: scales, normalize: h.Normalize, maxTokens: h.MaxTokens}
	if m.maxTokens > 0 {
		m.maxChars = m.maxTokens * h.Median
	}
	return m, nil
}

// token is the text of vocabulary id (a substring of the blob, no copy).
func (t *tokenizer) token(id uint32) string {
	return t.blob[t.off[id]:t.off[id+1]]
}

// cmpPrefixed compares tok with p+s without building p+s.
func cmpPrefixed(tok, p, s string) int {
	if len(tok) >= len(p) {
		if c := strings.Compare(tok[:len(p)], p); c != 0 {
			return c
		}
		return strings.Compare(tok[len(p):], s)
	}
	if c := strings.Compare(tok, p[:len(tok)]); c != 0 {
		return c
	}
	return -1 // tok is a proper prefix of p
}

// find is the id of the vocabulary token p+s (binary search).
func (t *tokenizer) find(p, s string) (int, bool) {
	lo, hi := 0, len(t.order)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if cmpPrefixed(t.token(t.order[mid]), p, s) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(t.order) && cmpPrefixed(t.token(t.order[lo]), p, s) == 0 {
		return int(t.order[lo]), true
	}
	return 0, false
}
