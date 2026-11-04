package validate

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

var hostDef = schema.Table("res_server", ids.PrefixResource,
	schema.Text("name", schema.Required()),
	schema.Field("ip", value.KindIP),
	schema.Number("port"),
	schema.Bool("public"),
	schema.Text("tier", schema.OneOf("dev", "prod")),
	schema.Text("password", schema.Sensitive()),
	schema.List("tags", value.KindText),
	schema.Ref("project_id", schema.TableProjects),
	schema.List("links", value.KindRef),
)

func txt(s string) value.Value {
	v, err := value.TextOf(s)
	if err != nil {
		panic(err)
	}
	return v
}

func ref(s string) value.Value {
	id, err := ids.ParseAny(s)
	if err != nil {
		panic(err)
	}
	r, err := value.RefOf(id)
	if err != nil {
		panic(err)
	}
	return r
}

const (
	prj = "prj_01936f0000017c3d8e4f5a6b7c8d9e0f"
	mem = "mem_01936f3300007d4e8f5a6b7c8d9e0f1a"
)

func TestFields(t *testing.T) {
	cases := []struct {
		name     string
		in       map[string]value.Value
		mode     Mode
		wantErr  []string // substrings of the one error
		wantKind map[string]value.Kind
	}{
		{name: "text converts to each kind", mode: Whole,
			in: map[string]value.Value{"name": txt("db"), "ip": txt("203.0.113.10"), "port": txt("5432"),
				"public": txt("true"), "tags": txt(`["a","b"]`), "project_id": txt(prj)},
			wantKind: map[string]value.Kind{"ip": value.KindIP, "port": value.KindNumber, "public": value.KindBool,
				"tags": value.KindList, "project_id": value.KindRef, "name": value.KindText}},
		{name: "typed values pass as they are", mode: Whole,
			in:       map[string]value.Value{"name": txt("db"), "port": value.Int(1)},
			wantKind: map[string]value.Kind{"port": value.KindNumber}},
		{name: "unknown field names the allowed ones", mode: Patch,
			in:      map[string]value.Value{"colour": txt("red")},
			wantErr: []string{"unknown field colour", "allowed: ip, links, name, password, port, project_id, public, tags, tier"}},
		{name: "required missing on whole", mode: Whole,
			in:      map[string]value.Value{"port": value.Int(1)},
			wantErr: []string{"field name is required"}},
		{name: "required may be absent in a patch", mode: Patch,
			in:       map[string]value.Value{"port": value.Int(1)},
			wantKind: map[string]value.Kind{"port": value.KindNumber}},
		{name: "required cannot be cleared", mode: Patch,
			in:      map[string]value.Value{"name": value.Null{}},
			wantErr: []string{"field name is required and cannot be cleared"}},
		{name: "optional can be cleared", mode: Patch,
			in:       map[string]value.Value{"port": value.Null{}},
			wantKind: map[string]value.Kind{"port": value.KindNull}},
		{name: "wrong kind names the wanted kind", mode: Patch,
			in:      map[string]value.Value{"ip": txt("not-an-ip"), "public": value.Int(3)},
			wantErr: []string{"field ip: want a ip value", "field public: want a bool value"}},
		{name: "closed set", mode: Patch,
			in:      map[string]value.Value{"tier": txt("staging")},
			wantErr: []string{"field tier: want one of dev, prod"}},
		{name: "list items converted", mode: Patch,
			in:       map[string]value.Value{"links": mustList(txt(prj), txt(mem))},
			wantKind: map[string]value.Kind{"links": value.KindList}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Fields(hostDef, tc.in, tc.mode)
			if len(tc.wantErr) > 0 {
				if !errs.Has(err, errs.ResourceInvalid) {
					t.Fatalf("err = %v, want E_RESOURCE_INVALID", err)
				}
				for _, w := range tc.wantErr {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("error %q lacks %q", err, w)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for k, want := range tc.wantKind {
				if got := out[k].Kind(); got != want {
					t.Errorf("%s kind = %s, want %s", k, got, want)
				}
			}
		})
	}
}

func TestErrorsNeverRepeatValues(t *testing.T) {
	secret := "hunter2-very-secret-value"
	_, err := Fields(hostDef, map[string]value.Value{"port": txt(secret), "nope": txt(secret), "tier": txt(secret)}, Patch)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error repeats the value or is nil: %v", err)
	}
}

func mustList(items ...value.Value) value.Value {
	l, err := value.ListOf(value.KindText, items...)
	if err != nil {
		panic(err)
	}
	return l
}

func TestTargetsAndDangling(t *testing.T) {
	def := schema.Table("notes", ids.PrefixMemory,
		schema.Ref("project_id", schema.TableProjects),
		schema.Ref("other_id", ""),
		schema.Text("target_type"),
		schema.Ref("target_id", ""),
		schema.List("links", value.KindRef),
		schema.PolymorphicRel("target", "target_type", "target_id", schema.TombstoneOnly),
	)
	def.Relations = append(def.Relations, schema.Relation{Name: "other", Kind: schema.BelongsTo, Field: "other_id", Target: schema.TableMemories, AllowDangling: true, OnDelete: schema.TombstoneOnly})
	l, err := value.ListOf(value.KindRef, ref(mem), ref(prj))
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{
		"project_id": ref(prj), "other_id": ref(mem), "target_type": txt("memories"), "target_id": ref(mem), "links": l,
	}
	got := Targets(def, fields)
	var lines []string
	for _, tg := range got {
		lines = append(lines, tg.Field+">"+string(tg.Table)+">"+string(tg.ID.Prefix())+">"+boolS(tg.AllowDangling))
	}
	want := "links>>mem>no,links>>prj>no,other_id>>mem>yes,project_id>projects>prj>no,target_id>memories>mem>no"
	if strings.Join(lines, ",") != want {
		t.Fatalf("targets\n got %s\nwant %s", strings.Join(lines, ","), want)
	}
	r := row.Row{Table: "notes", Fields: fields}
	d := Dangling(def, r, func(tg Target) bool { return tg.ID.Prefix() == ids.PrefixProject })
	if len(d) != 3 {
		t.Fatalf("dangling = %d, want 3 (the mem refs)", len(d))
	}
	r.Deleted, r.Fields = true, nil
	if len(Dangling(def, r, func(Target) bool { return false })) != 0 {
		t.Fatal("a tombstone has no references")
	}
}

func boolS(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
