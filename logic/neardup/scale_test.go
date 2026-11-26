package neardup

// scale_test.go — devco:D-1016 (bug hunt H-278): near_duplicate on a big
// store. Bodies drawn from a small shared vocabulary made every note a
// candidate of every other (2k notes 0.24 s, 16k 11 s, so tens of minutes
// for the 170k notes of a 1M-commit store); the document-frequency cap
// (postingCap) keeps the work near linear, and copies still pair.

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/testtime"
)

// smallVocab is a generated store's word list: every body draws from it.
var smallVocab = strings.Fields(`deploy invoice billing release cache index schema
migration rollback customer webhook queue retry timeout token session
report export import payment refund order cart search ranking backup
restore staging production monitor alert latency budget roadmap review
onboarding contract vendor audit backlog sprint incident postmortem`)

// vocabNotes is n notes as a generated store writes them: 6 to 14 words
// of smallVocab, then "(note <n>)".
func vocabNotes(n int) []Item {
	out := make([]Item, n)
	for s := range n {
		var b strings.Builder
		for i := range 6 + s%9 {
			b.WriteString(smallVocab[(s*7+i*13)%len(smallVocab)] + " ")
		}
		fmt.Fprintf(&b, "(note %d)", s)
		out[s] = Item{Table: "memories", ID: "mem_" + strconv.Itoa(s), Body: b.String()}
	}
	return out
}

// zipfNotes is n notes of 20 to 60 words drawn from a 50k-word Zipf
// vocabulary (natural text: a few words in most bodies, most words rare).
func zipfNotes(n int, seed uint64) []Item {
	r := rand.New(rand.NewPCG(seed, 7))
	z := rand.NewZipf(r, 1.1, 2, 50000)
	out := make([]Item, n)
	for s := range n {
		var b strings.Builder
		for range 20 + r.IntN(41) {
			b.WriteString("w" + strconv.FormatUint(z.Uint64(), 36) + " ")
		}
		out[s] = Item{Table: "memories", ID: "mem_" + strconv.Itoa(s), Body: b.String()}
	}
	return out
}

// storeWithin is WithinStore over items (one Vocab, their order).
func storeWithin(items []Item) []Match {
	return WithinStore(NewVocab().docs(items))
}

// TestWithinSmallVocabularyNearLinear: 20k notes of one small vocabulary
// are checked in well under the seconds the old join took (16k: 11 s),
// and the planted copies (same words, any order and case) still pair.
func TestWithinSmallVocabularyNearLinear(t *testing.T) {
	items := vocabNotes(20000)
	plant := []int{3, 4711, 19998}
	for i, p := range plant {
		ws := strings.Fields(items[p].Body)
		for a, b := 0, len(ws)-1; a < b; a, b = a+1, b-1 {
			ws[a], ws[b] = ws[b], ws[a]
		}
		items = append(items, Item{Table: "memories", ID: "copy_" + strconv.Itoa(i), Body: strings.ToUpper(strings.Join(ws, "  "))})
	}
	t0 := time.Now()
	got := storeWithin(items)
	took := time.Since(t0)
	if bar := testtime.SpeedBar(3 * time.Second); took > bar {
		t.Fatalf("Within over %d notes took %s, want under %s (near linear)", len(items), took, bar)
	}
	pairs := map[string]string{}
	for _, m := range got {
		pairs[m.New.ID] = m.Existing.ID
	}
	for i, p := range plant {
		if want := "mem_" + strconv.Itoa(p); pairs["copy_"+strconv.Itoa(i)] != want {
			t.Errorf("copy_%d paired with %q, want %s", i, pairs["copy_"+strconv.Itoa(i)], want)
		}
	}
}

// TestWithinRareWordCopiesPairInABigStore: in a big store two bodies that
// share most words, rare ones among them, still pair when their common
// words are over the cap; a word in every body brings no candidate.
func TestWithinRareWordCopiesPairInABigStore(t *testing.T) {
	items := zipfNotes(30000, 1)
	orig := items[1234].Body + " zebrafinch quokka"
	items[1234].Body = orig
	items = append(items, Item{Table: "memories", ID: "rewrite", Body: orig + " quokka"})
	items = append(items, Item{Table: "rules", ID: "other_table", Body: orig})
	got := storeWithin(items)
	var found bool
	for _, m := range got {
		if m.New.ID == "rewrite" {
			found = m.Existing.ID == "mem_1234" && m.Why == SimilarBody
		}
		if m.New.ID == "other_table" || m.Existing.ID == "other_table" {
			t.Errorf("a rule paired with a memory: %+v", m)
		}
	}
	if !found {
		t.Fatalf("the rewrite of mem_1234 did not pair (%d pairs)", len(got))
	}
}

// TestPostingCap: small sets keep every word (findings as before the
// cap), big ones cap at 1% within the bounds.
func TestPostingCap(t *testing.T) {
	for n, want := range map[int]int{10: MinPostings, 5000: MinPostings, 20000: 200, 170000: MaxPostings, 15000: 150} {
		if got := postingCap(n); got != want {
			t.Errorf("postingCap(%d) = %d, want %d", n, got, want)
		}
	}
}

// TestNearDupScale prints Within's time per size (NEARDUP_SCALE=1; the
// devco:D-1016 bench: must grow near linearly from 2k to 170k notes).
func TestNearDupScale(t *testing.T) {
	if os.Getenv("NEARDUP_SCALE") == "" {
		t.Skip("NEARDUP_SCALE is not set")
	}
	for _, n := range scaleSizes() {
		for name, items := range map[string][]Item{"vocab": vocabNotes(n), "zipf": zipfNotes(n, 2)} {
			t0 := time.Now()
			m := storeWithin(items)
			t.Logf("%-5s %6d notes: %8s, %d pairs", name, n, time.Since(t0).Round(time.Millisecond), len(m))
		}
	}
}

// scaleSizes is the note counts TestNearDupScale times (NEARDUP_SIZES,
// comma separated, else 2k to 170k).
func scaleSizes() []int {
	sizes := []int{2000, 4000, 8000, 16000, 32000, 64000, 170000}
	if s := os.Getenv("NEARDUP_SIZES"); s != "" {
		sizes = nil
		for _, f := range strings.Split(s, ",") {
			if n, err := strconv.Atoi(f); err == nil {
				sizes = append(sizes, n)
			}
		}
	}
	return sizes
}

// TestIndexFindsAsFind: an Index prepared once answers each incoming row
// as Find does (lore docs import checks every drafted block against the
// stored notes through one Index, devco:D-1016), call after call.
func TestIndexFindsAsFind(t *testing.T) {
	existing := zipfNotes(3000, 3)
	existing = append(existing, Item{Table: "memories", ID: "titled", Title: "Use portless for dev URLs", Body: "short"})
	idx := NewIndex(existing)
	probes := []Item{
		{Table: "memories", ID: "p1", Body: existing[42].Body + " extra"},
		{Table: "memories", ID: "p2", Title: "use PORTLESS for dev urls!", Body: "other"},
		{Table: "memories", ID: "p3", Body: "nothing like any stored note at all here"},
		{Table: "rules", ID: "p4", Body: existing[7].Body},
		{Table: "memories", ID: "p5", Body: strings.ToUpper(existing[1500].Body)},
	}
	for range 2 { // the stamps of one call must not leak into the next
		for _, p := range probes {
			want, got := Find([]Item{p}, existing), idx.Find([]Item{p})
			if fmt.Sprint(pairsOf(want)) != fmt.Sprint(pairsOf(got)) {
				t.Errorf("%s: Index %v, Find %v", p.ID, pairsOf(got), pairsOf(want))
			}
		}
	}
	if got := pairsOf(idx.Find(probes)); len(got) != 3 {
		t.Errorf("all probes at once: %v, want p1, p2 and p5 paired", got)
	}
}

// pairsOf is each match as "new~existing why percent".
func pairsOf(ms []Match) []string {
	var out []string
	for _, m := range ms {
		out = append(out, fmt.Sprintf("%s~%s %s %d", m.New.ID, m.Existing.ID, m.Why, m.Percent))
	}
	return out
}

// TestWithinStaysExact: Within (render's hidden pinned rows, lint
// --context) pairs exactly as before the bounded join (devco:D-1016 bounds
// only a whole store's check): on 600 notes of one small vocabulary, where
// every word is in more bodies than the cap, it finds each note's best
// match by brute force, while WithinStore pairs only the same word sets.
func TestWithinStaysExact(t *testing.T) {
	items := vocabNotes(600)
	for i := range items { // drop the note number: notes then differ only in their vocabulary words
		items[i].Body = items[i].Body[:strings.LastIndex(items[i].Body, "(")]
	}
	sets := make([][]uint32, len(items))
	v := NewVocab()
	for i, it := range items {
		sets[i] = v.Doc(it).words
	}
	type pair struct{ a, b int }
	want := map[pair]bool{}
	for i := range items {
		best, score := -1, 0.0
		for j := range items {
			if j == i || len(sets[i]) < MinWords || len(sets[j]) < MinWords {
				continue
			}
			if s := jaccard(sets[i], sets[j]); s >= Threshold && s > score {
				best, score = j, s
			}
		}
		if best >= 0 {
			want[pair{min(i, best), max(i, best)}] = true
		}
	}
	got := map[pair]bool{}
	at := map[string]int{}
	for i, it := range items {
		at[it.ID] = i
	}
	for _, m := range Within(items) {
		got[pair{at[m.Existing.ID], at[m.New.ID]}] = true
	}
	if len(want) == 0 || len(got) != len(want) {
		t.Fatalf("Within: %d pairs, brute force %d", len(got), len(want))
	}
	for p := range want {
		if !got[p] {
			t.Errorf("Within misses %s~%s", items[p.a].ID, items[p.b].ID)
		}
	}
	if b := len(storeWithin(items)); b >= len(want) {
		t.Errorf("WithinStore: %d pairs, want fewer than the exact %d (only same word sets)", b, len(want))
	}
}
