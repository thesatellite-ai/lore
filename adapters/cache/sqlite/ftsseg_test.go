package sqlite

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestSearchCJKAndEmoji is I-5: a Chinese or Japanese word inside a
// sentence (no spaces) is found, a Korean word with a particle attached
// is found, an emoji is found (with and without its skin tone, and a
// joined emoji by the whole sequence), and the snippet shows the row's own
// text with no segment marks. Words of other scripts, stemming and
// `code` in backticks behave as before.
func TestSearchCJKAndEmoji(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	cjk, emoji, latin := newDec(), newDec(), newDec()
	f.apply(c, store.SourceRepo, "1",
		f.decision(cjk, "数据库迁移规则：不要在生产环境直接修改表 日本語のテストです 한국어를 테스트", "x"),
		f.decision(emoji, "Deploy \U0001F680 \U0001F44D\U0001F3FD \U0001F469\u200d\U0001F4BB", "x"),
		f.decision(latin, "running the `lore` migrations at 25°C", "naïve café"),
	)
	for _, tc := range []struct {
		query string
		want  int // hits; 1 = the one row named by the case
	}{
		{"数据库迁移", 1}, {"迁移", 1}, {"生产环境", 1}, {"数据库迁移规则", 1}, {"迁移*", 1},
		{"日本語", 1}, {"テスト", 1}, {"한국어", 1},
		{"🚀", 1}, {"👍", 1}, {"👍🏽", 1}, {"👩\u200d💻", 1}, {"💻", 1},
		{"迁规", 0}, {"🦄", 0}, {"👍🏿", 0},
		{"lore", 1}, {"run", 1}, {"25", 1}, {"naive", 1}, {"Deploy", 1},
	} {
		hits, err := c.SearchHits(f.ctx, schema.TableDecisions, tc.query, 10)
		if err != nil {
			t.Errorf("search %q: %v", tc.query, err)
			continue
		}
		if len(hits) != tc.want {
			t.Errorf("search %q: %d hits, want %d", tc.query, len(hits), tc.want)
			continue
		}
		for _, h := range hits {
			if strings.ContainsRune(h.Snippet, ftsSegMark) {
				t.Errorf("search %q: snippet %+q holds a segment mark", tc.query, h.Snippet)
			}
		}
	}
	hits := must(c.SearchHits(f.ctx, schema.TableDecisions, "迁移", 10))
	if len(hits) != 1 || hits[0].ID != cjk || !strings.Contains(hits[0].Snippet, "数据库<b>迁移</b>规则") {
		t.Fatalf("迁移 hit %+v, want the CJK row with 数据库<b>迁移</b>规则", hits)
	}
}

// TestFTSSegment pins the marks: between CJK characters and around each
// emoji or symbol, never next to a space, quote or '*' (query syntax),
// and ASCII text is returned as is.
func TestFTSSegment(t *testing.T) {
	t.Parallel()
	const m = "\u200b"
	for _, tc := range []struct{ in, want string }{
		{"plain ascii text", "plain ascii text"},
		{"迁移*", "迁" + m + "移*"},
		{`"数据" OR db`, `"数` + m + `据" OR db`},
		{"deploy🚀now", "deploy" + m + "🚀" + m + "now"},
		{"👍🏽", "👍" + m + "🏽"},
		{"👩\u200d💻", "👩\u200d💻"},
		{"한국어를", "한" + m + "국" + m + "어" + m + "를"},
		{"25°C", "25" + m + "°" + m + "C"},
	} {
		if got := ftsSegment(tc.in); got != tc.want {
			t.Errorf("ftsSegment(%+q) = %+q, want %+q", tc.in, got, tc.want)
		}
	}
}

// TestSearchBigBodyFast is I-6: a search whose hit is a 1 MB body holding
// the word on every line took ~6 s (FTS5 snippet() is about matches x
// tokens); it now stays well under a second and the snippet is a short,
// balanced cut around the first match.
func TestSearchBigBodyFast(t *testing.T) {
	if raceDetector {
		// The bound is about wall time; under -race it measured the detector
		// (5.3 s at best on the after-land gate). The plain test run keeps it.
		t.Skip("wall-time bound: not meaningful under the race detector; the plain test run checks it")
	}
	t.Parallel()
	f := newFixture(t)
	c := f.open("c.db")
	big, small := newDec(), newDec()
	body := strings.Repeat("lorem ipsum ünï dolor sit amet consectetur\n", 23000) // ~1 MB
	f.apply(c, store.SourceRepo, "1",
		f.decision(big, "a big one", body),
		f.decision(small, "a small one", "lorem ipsum ünï dolor"),
	)
	// Best of three, under a bound loose enough for a loaded test machine
	// (3.5 s was seen at load 108 on a 48-core box): alone the search takes
	// under 0.1 s; the old quadratic path took 6 s or more even idle, and
	// far more under that load, so 5 s still catches it.
	var hits []SearchHit
	best := time.Hour
	for range 3 {
		start := time.Now()
		hits = must(c.SearchHits(f.ctx, schema.TableDecisions, "ünï", 10))
		best = min(best, time.Since(start))
	}
	if best > 5*time.Second {
		t.Fatalf("search took %v at best, want well under 1s", best)
	}
	if len(hits) != 2 {
		t.Fatalf("%d hits, want 2", len(hits))
	}
	for _, h := range hits {
		if len(h.Snippet) > 400 || strings.Count(h.Snippet, snippetOpen) != strings.Count(h.Snippet, snippetClose) || !strings.Contains(h.Snippet, "<b>ünï</b>") {
			t.Errorf("%v snippet %q: want a short balanced cut around <b>ünï</b>", h.ID, h.Snippet)
		}
	}
}

// TestTextChunks pins the chunking of a big row: every piece at most the
// size, cut after whitespace when there is one near the end, and the
// pieces add up to the text.
func TestTextChunks(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("lorem ipsum ünï dolor\n", 500) + strings.Repeat("数据库迁移", 900)
	chunks := textChunks(text, 1024)
	if strings.Join(chunks, "") != text {
		t.Fatal("chunks do not add up to the text")
	}
	for i, ch := range chunks {
		if len(ch) > 1024 || !utf8.ValidString(ch) {
			t.Errorf("chunk %d: %d bytes, valid %v", i, len(ch), utf8.ValidString(ch))
		}
		if i < 10 && !strings.HasSuffix(ch, "\n") && !strings.HasSuffix(ch, " ") {
			t.Errorf("chunk %d ends mid-word: %q", i, ch[max(0, len(ch)-10):])
		}
	}
}
