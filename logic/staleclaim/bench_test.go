package staleclaim

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/hybrid"
)

// storeSized is a store of 2,000 notes of about 1 kB, every tenth one
// planning something, and the 300 rows a question read.
func storeSized() ([]Doc, []string) {
	docs := make([]Doc, 2000)
	ids := make([]string, 0, 300)
	for i := range docs {
		body := strings.Repeat(fmt.Sprintf("The acme widget service %d handles deliveries for gadget%d and logs each step. ", i, i%97), 12)
		if i%10 == 0 {
			body = fmt.Sprintf("Planned: a retry queue for widget%d deliveries (not built yet). ", i) + body
		}
		docs[i] = Doc{ID: fmt.Sprintf("mem_%04d", i), Body: body, At: day(1 + i%27)}
		if i%7 == 0 && len(ids) < 300 {
			ids = append(ids, docs[i].ID)
		}
	}
	return docs, ids
}

// BenchmarkFindAskShaped is lore ask's cost per call: the corpus's BM25
// postings (built once per kept corpus, outside the loop) name the rows
// holding each word.
func BenchmarkFindAskShaped(b *testing.B) {
	docs, ids := storeSized()
	hd := make([]hybrid.Doc, len(docs))
	for i, d := range docs {
		hd[i] = hybrid.Doc{Text: d.Title + "\n" + d.Body}
	}
	ix := hybrid.Build(hd)
	b.ResetTimer()
	for b.Loop() {
		NewIndex(docs, ix.Holders).Find(ids)
	}
}

// BenchmarkFindAllLintShaped is lore lint's cost: every row checked, no
// postings.
func BenchmarkFindAllLintShaped(b *testing.B) {
	docs, _ := storeSized()
	b.ResetTimer()
	for b.Loop() {
		NewIndex(docs, nil).FindAll()
	}
}
