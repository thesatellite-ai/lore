package batch

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/testtime"
	"github.com/thesatellite-ai/lore/logic/docnotes"
	"github.com/thesatellite-ai/lore/logic/neardup"
	"github.com/thesatellite-ai/lore/schema"
)

// TestDocsImportBigStoreFast (devco:D-1016, bug hunt H-278): importing a
// doc of a few hundred paragraphs into a store of 20,000 notes compares
// every paragraph with the stored notes prepared once (neardup.Index):
// each paragraph used to prepare all 20,000 again (minutes for a big
// doc); the stored copy is still skipped.
func TestDocsImportBigStoreFast(t *testing.T) {
	var existing []neardup.Item
	for i := range 20000 {
		existing = append(existing, neardup.Item{Table: string(schema.TableMemories), ID: "mem_" + strconv.Itoa(i),
			Body: fmt.Sprintf("stored note %d explains how service s%d talks to queue q%d and retries r%d times", i, i, i%97, i%7)})
	}
	var doc strings.Builder
	doc.WriteString("# Big\n\n## Notes\n\n")
	for i := range 400 {
		fmt.Fprintf(&doc, "paragraph %d says the importer kept window w%d open for batch b%d of team t%d.\n\n", i, i, i%13, i%5)
	}
	doc.WriteString(existing[777].Body + "\n")
	var drafts []draft
	for _, p := range docnotes.Split(doc.String()) {
		drafts = append(drafts, newDraft(p, "BIG.md", t.TempDir(), t.TempDir()))
	}
	t0 := time.Now()
	_, skipped := dropRepeats(drafts, existing)
	if took, bar := time.Since(t0), testtime.SpeedBar(5*time.Second); took > bar {
		t.Fatalf("400 paragraphs against 20,000 stored notes took %s, want under %s", took, bar)
	}
	if len(skipped) != 1 || skipped[0].Existing != "mem_777" {
		t.Fatalf("skipped %+v, want only the stored paragraph (mem_777)", skipped)
	}
}
