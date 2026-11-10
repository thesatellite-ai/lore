package schema

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/formatgate"
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
	if mem.Prefix != ids.PrefixMemory || !mem.Synced || len(mem.FTS) != 4 { // body, source_ref, kind, source_kind (v0.1.17 FTS columns)
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
		"m2m join clash": {base, Table("others", ids.PrefixTeam, RefList("t_ids", "things"), ManyToManyRel("t", "t_ids", "things", "things", Cascade))},
		"m2m no field":   {base, Table("others", ids.PrefixTeam, ManyToManyRel("t", "t_ids", "things", "o_t", Cascade))},
		"m2m not list":   {base, Table("others", ids.PrefixTeam, Ref("t_ids", "things"), ManyToManyRel("t", "t_ids", "things", "o_t", Cascade))},
		"m2m text list":  {base, Table("others", ids.PrefixTeam, List("t_ids", value.KindText), ManyToManyRel("t", "t_ids", "things", "o_t", Cascade))},
		"m2m any table":  {base, Table("others", ids.PrefixTeam, RefList("t_ids", ""), ManyToManyRel("t", "t_ids", "things", "o_t", Cascade))},
		"m2m wrong ref":  {base, Table("others", ids.PrefixTeam, RefList("t_ids", "others"), ManyToManyRel("t", "t_ids", "things", "o_t", Cascade))},
		"m2m join shared": {base, Table("others", ids.PrefixTeam, RefList("a_ids", "things"), RefList("b_ids", "things"),
			ManyToManyRel("a", "a_ids", "things", "o_t", Cascade), ManyToManyRel("b", "b_ids", "things", "o_t", Cascade))},
		"purge-with on belongs": {Table("things", ids.PrefixTag, Ref("p_id", "things"),
			relation(Relation{Name: "p", Kind: BelongsTo, Field: "p_id", Target: "things", OnDelete: TombstoneOnly, PurgeWithTarget: true}))},
		"ref table on text": {Table("things", ids.PrefixTag, Field("x", value.KindText, func(f *FieldDef) { f.RefTable = "things" }))},
		"poly no type":      {Table("things", ids.PrefixTag, Ref("o_id", ""), PolymorphicRel("o", "o_type", "o_id", TombstoneOnly))},
		"bad on delete":     {Table("things", ids.PrefixTag, Ref("p_id", "things"), BelongsToRel("p", "p_id", "things", "explode"))},
	}
	for name, defs := range cases {
		if _, err := NewRegistry(defs...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := []TableDef{
		base,
		Table("others", ids.PrefixTeam,
			Ref("thing_id", "things"), Ref("parent_id", "others"), Text("o_type"), Ref("o_id", ""), RefList("thing_ids", "things"),
			BelongsToRel("thing", "thing_id", "things", Cascade),
			SelfRel("parent", "parent_id", SetNull),
			ManyToManyRel("tags", "thing_ids", "things", "others_things", Cascade),
			PolymorphicRel("owner", "o_type", "o_id", TombstoneOnly),
		),
	}
	if _, err := NewRegistry(ok...); err != nil {
		t.Fatalf("every relation kind: %v", err)
	}
}

// TestBuiltinTags: every table that carries tags has a tag_ids list of
// refs to tags and a many-to-many over it with its own join table (O11),
// and no join table is a registered table.
func TestBuiltinTags(t *testing.T) {
	t.Parallel()
	r, err := NewRegistry(Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	tags, ok := r.Table(TableTags)
	if !ok || tags.Prefix != ids.PrefixTag || !tags.Synced {
		t.Fatalf("tags %+v", tags)
	}
	want := map[string]string{TableActors: JoinActorTags, TableProjects: JoinProjectTags, TableRepos: JoinRepoTags, TableMemories: JoinMemoryTags}
	for table, join := range want {
		def, _ := r.Table(row.Table(table))
		f, ok := def.Field(FieldTagIDs)
		if !ok || f.Kind != value.KindList || f.Elem != value.KindRef || f.RefTable != TableTags {
			t.Errorf("%s.%s = %+v", table, FieldTagIDs, f)
		}
		found := false
		for _, rel := range def.Relations {
			if rel.Kind == ManyToMany && rel.Name == RelTags && rel.Field == FieldTagIDs && rel.Target == TableTags && rel.JoinTable == row.Table(join) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no tags relation through %s", table, join)
		}
		if _, clash := r.Table(row.Table(join)); clash {
			t.Errorf("join %s is a registered table", join)
		}
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

// TestKnowledgeRefsDanglingProject: knowledge_refs' dst_project may point
// at a project no attached store holds (a cross-project ref, v0.1.17
// accepted it); src and dst stay dangling-allowed too.
func TestKnowledgeRefsDanglingProject(t *testing.T) {
	t.Parallel()
	r, err := NewRegistry(Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	def, ok := r.Table(TableKnowledgeRefs)
	if !ok {
		t.Fatal("no knowledge_refs")
	}
	want := map[string]bool{"src": true, "dst": true, "dst_project": true}
	for _, rel := range def.Relations {
		if want[rel.Name] && !rel.AllowDangling {
			t.Errorf("%s does not allow a dangling target", rel.Name)
		}
		delete(want, rel.Name)
	}
	if len(want) != 0 {
		t.Errorf("relations missing: %v", want)
	}
}

// updateFormat rewrites the format snapshots:
// go test ./schema -run 'Format|Enum' -update-format
var updateFormat = flag.Bool("update-format", false, "rewrite testdata/format/*.golden")

// formatSnapshot is the synced columns per table (name, kind, element
// kind, required), one line per column, sorted — the file format of every
// synced row (FORMAT_EVOLUTION rule 5).
func formatSnapshot(t *testing.T, enums bool) string {
	t.Helper()
	var lines []string
	for _, d := range Builtin() {
		if !d.Synced {
			continue
		}
		for _, f := range d.Fields {
			if enums {
				if len(f.Values) > 0 {
					lines = append(lines, d.Name.String()+"."+f.Name+" "+strings.Join(f.Values, ","))
				}
				continue
			}
			l := d.Name.String() + "." + f.Name + " " + string(f.Kind)
			if f.Elem != "" {
				l += "[" + string(f.Elem) + "]"
			}
			if f.Required {
				l += " required"
			}
			lines = append(lines, l)
		}
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n") + "\n"
}

// checkGolden compares got with testdata/format/<name>.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "format", name)
	if *updateFormat {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("format change not classified: a synced column or enum value changed (%s).\n"+
			"Classify it (FORMAT_EVOLUTION rule 5: additive, or a Step in model/row/steps.go with a feature), "+
			"then run: go test ./schema -run 'Format|Enum' -update-format", path)
	}
}

// TestFormatSchemaSnapshot: every synced column change is seen (rule 5).
func TestFormatSchemaSnapshot(t *testing.T) {
	checkGolden(t, "schema.golden", formatSnapshot(t, false))
}

// TestEnumSnapshot: every new value of a synced closed set is seen (an old
// lore that checks the set would refuse it, rule 5).
func TestEnumSnapshot(t *testing.T) { checkGolden(t, "enum.golden", formatSnapshot(t, true)) }

// TestFormatgateTableNames: logic/formatgate pins these table names
// (it cannot import schema at the same layer rank).
func TestFormatgateTableNames(t *testing.T) {
	if formatgate.TablePolicies != TablePolicies || formatgate.TablePlans != TablePlans || formatgate.KindTablePrefix != KindTablePrefix {
		t.Fatal("formatgate table names drifted from schema")
	}
	if formatgate.TableTasks != TableTasks {
		t.Fatal("formatgate tasks table drifted from schema")
	}
	want := []string{FieldTaskParent, FieldTaskRank, FieldTaskCloseReason, FieldTaskChecklist, FieldTaskAssignees, FieldTaskWatchers, FieldTaskEstimate, FieldTaskRecur, FieldTaskHistory}
	if !slices.Equal(formatgate.TaskTreeFields(), want) {
		t.Fatalf("formatgate.TaskTreeFields %v drifted from schema %v", formatgate.TaskTreeFields(), want)
	}
	i := slices.IndexFunc(Builtin(), func(d TableDef) bool { return d.Name.String() == TableTasks })
	if i < 0 {
		t.Fatal("no tasks table")
	}
	tasks := Builtin()[i]
	for _, f := range want {
		if _, has := tasks.Field(f); !has {
			t.Errorf("tasks has no field %s", f)
		}
	}
}
