package engine

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// urlSweepExempt are built-in fields whose name mentions a URL but that
// are not stored through stripURLs' name rule, each with the path that
// strips them instead (D27: a new one must be added here on purpose).
var urlSweepExempt = map[string]string{
	"repos.urls":   "the repo URL history (PROV1-3): every entry is written by ops/infra through repourl.StripCredentials (repo add, set-url, remote detection)",
	"kinds.origin": "a json provenance record of a kind definition, not a URL",
}

// TestURLFieldClassSweep is the D27 class sweep: every built-in table
// field whose name or kind marks a URL must be one the write-time strip
// (stripURLs) covers: kind url (value.URLOf strips it) or text with a
// URL name (repourl.URLFieldName). A new URL-ish field that neither rule
// catches fails here until it is routed through StripCredentials.
func TestURLFieldClassSweep(t *testing.T) {
	looksURL := func(n string) bool {
		n = strings.ToLower(n)
		return strings.Contains(n, "url") || strings.Contains(n, "remote") || strings.Contains(n, "origin") || strings.Contains(n, "href") || strings.Contains(n, "link_to")
	}
	checked := 0
	for _, def := range schema.Builtin() {
		for _, f := range def.Fields {
			if !looksURL(f.Name) && f.Kind != value.KindURL {
				continue
			}
			checked++
			key := string(def.Name) + "." + f.Name
			covered := f.Kind == value.KindURL || (f.Kind == value.KindText && repourl.URLFieldName(f.Name))
			if _, ok := urlSweepExempt[key]; ok || covered {
				continue
			}
			t.Errorf("%s (kind %s) looks like a URL but the write-time strip does not cover it: name it *_url or make it kind url, or add it to urlSweepExempt with the path that strips it", key, f.Kind)
		}
	}
	if checked == 0 {
		t.Fatalf("the sweep found no URL field at all: the walk is broken")
	}
}
