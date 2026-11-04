package schema

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

func TestBuiltinRegistryValid(t *testing.T) {
	t.Parallel()
	r, err := NewRegistry(Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{TableMemories, TablePolicies, TableMetaKeys, TableMetaValues, TableKinds, TableKindFields, TableVars, TableSecretRules, TableSecretAllows, TableResources} {
		if _, ok := r.Table(row.Table(name)); !ok {
			t.Errorf("missing %s", name)
		}
	}
	mem, _ := r.Table(TableMemories)
	if mem.Prefix != ids.PrefixMemory || !mem.Synced || len(mem.FTS) != 1 {
		t.Fatalf("memories %+v", mem)
	}
	if d, ok := r.ByPrefix(ids.PrefixVar); !ok || d.Name != TableVars {
		t.Fatal("ByPrefix")
	}
}

func TestRegistryRejects(t *testing.T) {
	t.Parallel()
	base := Table("things", ids.PrefixTag, Text("name"))
	cases := map[string][]TableDef{
		"duplicate table":  {base, base},
		"duplicate prefix": {base, Table("others", ids.PrefixTag)},
		"bad name":         {Table("Things", ids.PrefixTag)},
		"bad prefix":       {Table("things", "zzz")},
		"reserved field":   {Table("things", ids.PrefixTag, Text("created_at"))},
		"dup field":        {Table("things", ids.PrefixTag, Text("a"), Text("a"))},
		"list no elem":     {Table("things", ids.PrefixTag, Field("a", value.KindList))},
		"null kind":        {Table("things", ids.PrefixTag, Field("a", value.KindNull))},
		"bad ref target":   {Table("things", ids.PrefixTag, Ref("x_id", "nowhere"))},
		"fts not text":     {Table("things", ids.PrefixTag, Number("n"), FTS("n"))},
		"index missing":    {Table("things", ids.PrefixTag, IndexOn("nope"))},
		"values on number": {Table("things", ids.PrefixTag, Number("n", OneOf("a")))},
		"bad widget":       {Table("things", ids.PrefixTag, Text("a", WithWidget("slider")))},
		"belongs no field": {Table("things", ids.PrefixTag, BelongsToRel("p", "p_id", "things", Restrict))},
		"set null required": {Table("things", ids.PrefixTag,
			Ref("p_id", "things", Required()), BelongsToRel("p", "p_id", "things", SetNull))},
		"unknown target": {Table("things", ids.PrefixTag, Ref("p_id", ""), BelongsToRel("p", "p_id", "nowhere", Restrict))},
		"has many field": {base, Table("others", ids.PrefixTeam, HasManyRel("t", "things", "nope"))},
		"m2m join clash": {base, Table("others", ids.PrefixTeam, ManyToManyRel("t", "things", "things", Cascade))},
		"poly no type":   {Table("things", ids.PrefixTag, Ref("o_id", ""), PolymorphicRel("o", "o_type", "o_id", TombstoneOnly))},
		"bad on delete":  {Table("things", ids.PrefixTag, Ref("p_id", "things"), BelongsToRel("p", "p_id", "things", "explode"))},
	}
	for name, defs := range cases {
		if _, err := NewRegistry(defs...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := []TableDef{
		base,
		Table("others", ids.PrefixTeam,
			Ref("thing_id", "things"), Ref("parent_id", "others"), Text("o_type"), Ref("o_id", ""),
			BelongsToRel("thing", "thing_id", "things", Cascade),
			SelfRel("parent", "parent_id", SetNull),
			ManyToManyRel("tags", "things", "others_things", Cascade),
			PolymorphicRel("owner", "o_type", "o_id", TombstoneOnly),
		),
	}
	if _, err := NewRegistry(ok...); err != nil {
		t.Fatalf("every relation kind: %v", err)
	}
}

func TestErrorsListEveryProblem(t *testing.T) {
	t.Parallel()
	_, err := NewRegistry(Table("Bad", "zzz", Text("created_at")))
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"snake_case", "prefix", "reserved"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func mustRow(t *testing.T, doc string) row.Row {
	t.Helper()
	r, err := row.Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const (
	kindID   = "knd_01936f3500007f6a8b7c8d9e0f1a2b3c"
	stamp    = `"_hlc":"2026-11-04T09:30:00.000Z/0000/"`
	kindDoc  = `{"_v":2,"_table":"kinds","id":"` + kindID + `",` + stamp + `,"name":"server","description":"A machine the team runs","name_format":"id","render":"off","origin":null}`
	fieldIP  = `{"_v":2,"_table":"kind_fields","id":"fld_01936f3500017f6a8b7c8d9e0f1a2b3c",` + stamp + `,"kind_id":"` + kindID + `","name":"ip","type":"ip","required":true,"label":"IP","in_list":true}`
	fieldReg = `{"_v":2,"_table":"kind_fields","id":"fld_01936f3500027f6a8b7c8d9e0f1a2b3c",` + stamp + `,"kind_id":"` + kindID + `","name":"region","type":"text"}`
	fieldDel = `{"_v":2,"_table":"kind_fields","id":"fld_01936f3500037f6a8b7c8d9e0f1a2b3c",` + stamp + `,"_deleted":true}`
)

func TestKindDefFromRows(t *testing.T) {
	t.Parallel()
	def, err := KindDefFromRows(mustRow(t, kindDoc), []row.Row{mustRow(t, fieldIP), mustRow(t, fieldReg), mustRow(t, fieldDel)})
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "res_server" || def.Prefix != ids.PrefixResource || def.Kind.String() != kindID || len(def.Fields) != 3 {
		t.Fatalf("def %+v", def)
	}
	ip, _ := def.Field("ip")
	if ip.Kind != value.KindIP || !ip.Required || ip.UI.Label != "IP" || ip.UI.InList != InListYes {
		t.Fatalf("ip field %+v", ip)
	}
	reg, err := NewRegistry(Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	withKind, err := reg.With(def)
	if err != nil {
		t.Fatalf("kind def does not join the registry: %v", err)
	}
	if _, ok := withKind.Table("res_server"); !ok {
		t.Fatal("kind table missing")
	}
	if _, ok := reg.Table("res_server"); ok {
		t.Fatal("With mutated the original registry")
	}
	if got, _ := withKind.ByPrefix(ids.PrefixResource); got.Name != TableResources {
		t.Fatal("ByPrefix must name the stored table, not a kind table")
	}
	ui := ResolveFields(def.Fields)
	if !ui[0].InList || ui[1].Label != "IP" || ui[2].Label != "Region" || ui[2].Widget != WidgetText {
		t.Fatalf("ui %+v", ui)
	}
}

func TestKindDefRefuses(t *testing.T) {
	t.Parallel()
	other := strings.Replace(fieldIP, kindID, "knd_01936f3500007f6a8b7c8d9e0f1a2b3d", 1)
	badType := strings.Replace(fieldReg, `"type":"text"`, `"type":"string"`, 1)
	notKind := mustRow(t, fieldReg)
	cases := map[string]func() error{
		"other kind": func() error { _, err := KindDefFromRows(mustRow(t, kindDoc), []row.Row{mustRow(t, other)}); return err },
		"bad type": func() error {
			_, err := KindDefFromRows(mustRow(t, kindDoc), []row.Row{mustRow(t, badType)})
			return err
		},
		"not kinds": func() error { _, err := KindDefFromRows(notKind, nil); return err },
	}
	for name, f := range cases {
		if f() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	dupName := strings.Replace(fieldReg, `"name":"region"`, `"name":"name"`, 1)
	def, err := KindDefFromRows(mustRow(t, kindDoc), []row.Row{mustRow(t, dupName)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(append(Builtin(), def)...); err == nil {
		t.Error("a kind field named name was accepted")
	}
}

func TestDefaultUI(t *testing.T) {
	t.Parallel()
	fields := []FieldDef{
		{Name: "name", Kind: value.KindText},
		{Name: "body", Kind: value.KindText, UI: UI{Widget: WidgetMarkdown}},
		{Name: "due_at", Kind: value.KindDate},
		{Name: "token", Kind: value.KindText, Sensitive: true},
		{Name: "a", Kind: value.KindText}, {Name: "b", Kind: value.KindNumber}, {Name: "c", Kind: value.KindBool},
		{Name: "d", Kind: value.KindText, Values: []string{"x"}},
	}
	got := ResolveFields(fields)
	want := []ResolvedUI{
		{"Name", WidgetText, true}, {"Body", WidgetMarkdown, false}, {"Due at", WidgetDate, true},
		{"Token", WidgetSecret, false}, {"A", WidgetText, true}, {"B", WidgetNumber, true},
		{"C", WidgetBool, true}, {"D", WidgetSelect, false},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: %+v want %+v", fields[i].Name, got[i], want[i])
		}
	}
}
