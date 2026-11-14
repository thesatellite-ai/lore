package lint

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestUntaggedFixIsARunnableCommand: the untagged finding's fix once said
// `tag add <tag> <id>`, which fails (tag add only creates a tag, by
// --name). The fix must attach through the noun's edit with named flags.
func TestUntaggedFixIsARunnableCommand(t *testing.T) {
	r := sourced{src: store.SourceRepo, r: row.Row{ID: id(t, ids.PrefixRule, 1), Fields: map[string]value.Value{
		"severity": tx(t, "must"), "body": tx(t, "A rule."), schema.FieldSummary: tx(t, "ok")}}}
	var fix string
	for _, f := range contextFindings(map[row.Table][]sourced{schema.TableRules: {r}}) {
		if f.Code == Untagged {
			fix = f.Fix
		}
	}
	if fix == "" {
		t.Fatal("no untagged finding for a pinned rule with no tag")
	}
	want := "lore rule edit --id " + r.r.ID.String() + " --tag <tag>"
	if !strings.HasPrefix(fix, want) {
		t.Errorf("fix = %q, want it to start with %q", fix, want)
	}
	if strings.Contains(fix, "tag add") && !strings.Contains(fix, "tag add --name") {
		t.Errorf("fix %q names `tag add` without --name", fix)
	}
}
