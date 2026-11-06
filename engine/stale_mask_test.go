package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestStaleMessageHidesSensitiveValues (DELETION 3.11, RQ1, METADATA
// 3.20): an E_STALE message repeats both values of a plain clash, and
// repeats neither for a clash on a resource's entry field (one entry or
// the whole object), a metadata entry whose label or key row says
// sensitive, or a metadata key id that does not parse; a plain metadata
// key's values are shown.
func TestStaleMessageHidesSensitiveValues(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	jsonOf := func(s string) value.JSON {
		t.Helper()
		j, err := value.JSONOf([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	entry := func(v string, sensitive bool) value.JSON {
		t.Helper()
		j, err := meta.Entry{Name: "k", Scope: "repo", V: []byte(`"` + v + `"`), Sensitive: sensitive}.Encode()
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	keyID := func(name string, sensitive bool) string {
		t.Helper()
		fields := metaKeyFields(name)
		fields[mkSensitive] = value.BoolOf(sensitive)
		w, err := f.eng.Create(ctx, "", schema.TableMetaKeys, fields)
		if err != nil {
			t.Fatal(err)
		}
		return w.Row.ID.String()
	}
	plainKey, secretKey := keyID("client", false), keyID("vpn_host", true)
	resDef, _ := f.eng.reg.Table(schema.TableResources)
	memDef, _ := f.eng.reg.Table(schema.TableMemories)
	ipA := jsonOf(`{"fld_01a1a5aca7f57659be7320c5f4b70011":{"name":"ip","v":"probe-ip-new"}}`)
	ipB := jsonOf(`{"fld_01a1a5aca7f57659be7320c5f4b70011":{"name":"ip","v":"probe-ip-old"}}`)
	cases := []struct {
		name   string
		def    schema.TableDef
		clash  merge.FieldConflict
		hidden bool
	}{
		{"plain text field", memDef, merge.FieldConflict{Field: "body", Newer: txt("probe-new"), Older: txt("probe-old")}, false},
		{"resource entry", resDef, merge.FieldConflict{Field: "fields.fld_01a1a5aca7f57659be7320c5f4b70011", Newer: ipA, Older: ipB}, true},
		{"resource whole fields", resDef, merge.FieldConflict{Field: "fields", Newer: ipA, Older: ipB}, true},
		{"meta entry labelled sensitive", memDef, merge.FieldConflict{Field: "meta." + plainKey, Newer: entry("probe-new", true), Older: entry("probe-old", false)}, true},
		{"meta key row sensitive", memDef, merge.FieldConflict{Field: "meta." + secretKey, Newer: entry("probe-new", false), Older: entry("probe-old", false)}, true},
		{"meta key id unreadable", memDef, merge.FieldConflict{Field: "meta.not-an-id", Newer: entry("probe-new", false), Older: entry("probe-old", false)}, true},
		{"meta plain key", memDef, merge.FieldConflict{Field: "meta." + plainKey, Newer: entry("probe-new", false), Older: entry("probe-old", false)}, false},
		{"entry of an undeclared field", memDef, merge.FieldConflict{Field: "extra.x", Newer: txt("probe-new"), Older: txt("probe-old")}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := f.eng.staleErr(ctx, f.project, []merge.FieldConflict{c.clash}, c.def).Error()
			leaked := strings.Contains(msg, "probe-")
			if leaked == c.hidden || !strings.Contains(msg, c.clash.Field) {
				t.Fatalf("hidden=%v: %s", c.hidden, msg)
			}
		})
	}
}
