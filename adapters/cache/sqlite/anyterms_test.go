package sqlite

import (
	"slices"
	"testing"
)

// TestAnyTerms: a question's any-word form (devco D-462) is read for the
// filled-in variable text, so `search pgmain` still finds a row whose
// ${var:…} holds it; anything else is not an any-word form.
func TestAnyTerms(t *testing.T) {
	for _, c := range []struct {
		match    string
		pos, neg []string
		ok       bool
	}{
		{`("pgmain" OR "pgmains")`, []string{"pgmain", "pgmains"}, nil, true},
		{`("deploy" OR "deploys") OR ("orbit") NOT "draft"`, []string{"deploy", "deploys", "orbit"}, []string{"draft"}, true},
		{`"pgmain"`, nil, nil, false},
		{`title:"a" OR "b"`, nil, nil, false},
		{`"a b" OR "c"`, nil, nil, false},
		{`deploy OR ship`, nil, nil, false},
		{`("a" OR "b") NOT`, nil, nil, false},
	} {
		pos, neg, ok := anyTerms(c.match)
		if ok != c.ok || (ok && (!slices.Equal(pos, c.pos) || !slices.Equal(neg, c.neg))) {
			t.Errorf("anyTerms(%s) = %v %v %v, want %v %v %v", c.match, pos, neg, ok, c.pos, c.neg, c.ok)
		}
	}
}
