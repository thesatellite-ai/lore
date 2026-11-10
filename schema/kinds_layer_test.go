package schema

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
)

// TestKindExtensions: a nearer level's extension row folds into its base
// field (flags OR-ed, no second field), and an extension that changes the
// type, replaces a pattern or widens a bound refuses the kind with
// E_KIND_CONFLICT (RES 3.6).
func TestKindExtensions(t *testing.T) {
	t.Parallel()
	ext := func(body string) row.Row {
		if !strings.Contains(body, `"name"`) {
			body += `,"name":"region"`
		}
		if !strings.Contains(body, `"type"`) {
			body += `,"type":"text"`
		}
		return mustRow(t, `{"_v":2,"_table":"kind_fields","id":"fld_01936f3500097f6a8b7c8d9e0f1a2b3c",`+stamp+
			`,"kind_id":"`+kindID+`","extends":"fld_01936f3500027f6a8b7c8d9e0f1a2b3c"`+body+`}`)
	}
	base := mustRow(t, `{"_v":2,"_table":"kind_fields","id":"fld_01936f3500027f6a8b7c8d9e0f1a2b3c",`+stamp+
		`,"kind_id":"`+kindID+`","name":"region","type":"text","pattern":"^[a-z]+$","min":"1","max":"9"}`)
	def, err := KindDefFromRows(mustRow(t, kindDoc), []row.Row{base, ext(`,"required":true,"sensitive":true,"min":"2"`)})
	if err != nil {
		t.Fatal(err)
	}
	f, ok := def.Field("region")
	if !ok || !f.Required || !f.Sensitive || len(def.Fields) != 2 {
		t.Fatalf("folded %+v (fields %d)", f, len(def.Fields))
	}
	for name, body := range map[string]string{
		"retype":       `,"type":"number"`,
		"new pattern":  `,"pattern":"^x"`,
		"wider min":    `,"min":"0"`,
		"wider max":    `,"max":"10"`,
		"rename":       `,"name":"zone"`,
		"bad min text": `,"min":"abc"`,
	} {
		if _, err := KindDefFromRows(mustRow(t, kindDoc), []row.Row{base, ext(body)}); !errs.Has(err, errs.KindConflict) {
			t.Errorf("%s: %v, want E_KIND_CONFLICT", name, err)
		}
	}
}
