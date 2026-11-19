package querywords_test

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/querywords"
)

// TestTerms (D-435): a question is read as its topic words: stop words
// out, end punctuation trimmed, inner punctuation kept, lower-case, no
// repeats; a query of stop words only keeps them.
func TestTerms(t *testing.T) {
	for q, want := range map[string][]string{
		"how do we deploy Orbit?":               {"deploy", "orbit"},
		"where is the Pinecrest staging server": {"pinecrest", "staging", "server"},
		"deploy.prod.yml":                       {"deploy.prod.yml"},
		"well-known, well-known":                {"well-known"},
		"the who":                               {"the", "who"},
		"迁移 规则":                                 {"迁移", "规则"},
		"   ":                                   nil,
	} {
		if got := querywords.Terms(q); !slices.Equal(got, want) {
			t.Errorf("Terms(%q) = %q, want %q", q, got, want)
		}
	}
}

// TestStemHas (D-435): word forms match each other through the light stem.
func TestStemHas(t *testing.T) {
	for w, want := range map[string]string{
		"deploying": "deploy", "deployment": "deploy", "deploys": "deploy", "deployed": "deploy",
		"running": "run", "locally": "local", "policies": "policy", "class": "class", "billing": "bill",
		"go": "go", "api": "api",
	} {
		if got := querywords.Stem(w); got != want {
			t.Errorf("Stem(%q) = %q, want %q", w, got, want)
		}
	}
	text := "Deploying orbit to staging in a storm: run it locally first"
	for _, term := range []string{"deployment", "deploys", "Orbit", "running", "locally", "stage"} {
		if !querywords.Has(text, term) {
			t.Errorf("Has(%q) = false", term)
		}
	}
	for _, term := range []string{"postgres", "stored", "or"} {
		if querywords.Has(text, term) {
			t.Errorf("Has(%q) = true", term) // "or" is inside orbit, stored is not storm's word
		}
	}
	if !querywords.Has("see deploy.prod.yml", "deploy.prod.yml") || !querywords.Has("数据库迁移规则", "迁移") {
		t.Error("a term that is not one plain word is not found as written")
	}
	if got := querywords.Coverage(text, []string{"deploy", "orbit", "postgres"}); got != 2 {
		t.Errorf("Coverage = %d, want 2", got)
	}
}

// TestPrefixTerm (devco D-465): Terms keeps a prefix word's '*' and
// Coverage counts a word starting with it.
func TestPrefixTerm(t *testing.T) {
	if got := querywords.Terms("invoi*"); len(got) != 1 || got[0] != "invoi*" {
		t.Fatalf("Terms(invoi*) = %v", got)
	}
	if querywords.Coverage("Invoice numbering", []string{"invoi*"}) != 1 || querywords.Coverage("Quarterly report", []string{"invoi*"}) != 0 {
		t.Error("prefix coverage")
	}
}
