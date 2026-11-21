package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/searchrank"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestMatchRefusesSyntax (devco D-475): Match, the one matcher of `lore
// search` and every `<kind> search`, keeps the cache's refusal of a query
// written in FTS5 syntax with no topic word (a lone quote, a dangling
// AND, a stray "(" or "*") or holding no word ('), so each search says
// why the query is not valid instead of finding nothing; plain words the
// index refuses (c++) are still retried quoted and find their rows.
// Syntax around topic words (`"unclosed`) is TestMatchReadsRefusedSyntaxAsWords.
func TestMatchRefusesSyntax(t *testing.T) {
	t.Parallel()
	refused := errs.New(errs.InvalidInput, "not a valid search query (fts5: syntax error)")
	var tried []string
	// the fake index accepts only fully quoted text, as FTS5 refuses each
	// of these queries written as is
	match := MatchFunc(func(_ context.Context, m string, _ int) ([]Hit, error) {
		tried = append(tried, m)
		if !strings.HasPrefix(m, `"`) || strings.Count(m, `"`)%2 != 0 {
			return nil, refused
		}
		return []Hit{{}}, nil
	})
	for _, q := range []string{`"`, `'`, `*`, `(`, `AND`, `a OR`, `NEAR(`, `NOT`, `a AND`} {
		tried = nil
		_, _, _, err := Match(context.Background(), match, searchrank.Of(q), 10)
		if !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%q: err %v after %q, want the E_INVALID_INPUT refusal", q, err, tried)
		}
	}
	hits, _, _, err := Match(context.Background(), match, searchrank.Of("c++"), 10)
	if err != nil || len(hits) != 1 {
		t.Errorf("c++: %d hits, %v; want the quoted retry's hit", len(hits), err)
	}
}

// TestMatchReadsRefusedSyntaxAsWords (devco D-798, P-14): a query FTS5
// refuses for punctuation around words (`alice (person)`, `orbit)`,
// `"unclosed`) is searched again as its plain words, and Match returns
// those words so the caller ranks the hits by them (any word, Natural).
// Before, every search failed with "fts5: syntax error near ...".
func TestMatchReadsRefusedSyntaxAsWords(t *testing.T) {
	t.Parallel()
	refused := errs.New(errs.InvalidInput, "not a valid search query (fts5: syntax error)")
	// the fake index refuses the punctuation FTS5 refuses outside a
	// quoted string: a parenthesis that does not group, ^, { or } and an
	// odd number of quotes
	match := MatchFunc(func(_ context.Context, m string, _ int) ([]Hit, error) {
		if strings.Count(m, `"`)%2 != 0 || strings.ContainsAny(m, "^{}") || strings.Contains(m, ")") && !strings.HasPrefix(m, "(") || strings.HasPrefix(m, "(") && !strings.Contains(m, ")") {
			return nil, refused
		}
		return []Hit{{}}, nil
	})
	for _, q := range []string{`alice (person)`, `orbit)`, `(acme`, `"unclosed`, `rate^`, `{orbit}`} {
		hits, _, w, err := Match(context.Background(), match, searchrank.Of(q), 10)
		if err != nil || len(hits) == 0 {
			t.Errorf("%q: %d hits, %v; want the plain words' hit", q, len(hits), err)
			continue
		}
		if w.Syntax {
			t.Errorf("%q: ranked with the refused syntax words %+v, want the plain words", q, w)
		}
	}
}
