package tag_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// v0.1.17 run (lore 0.1.17 `lore tag add --name=cert_rotation` in project
// v017Project) wrote the tag file v017Tag: a tag's id is the natural id of
// (project_id, name), and the repo store keeps v0.1.17's bytes.
const (
	v017Project = "prj_01a1a67920bd71b197afabd27d76918c"
	v017Tag     = "tag_7aab3ed507407f139f49d934fee0e558"
)

// tagTables lists every built-in table that carries tags (the tags
// many-to-many on tag_ids), read from the schema so a new tagged table is
// covered without editing this test.
func tagTables(t *testing.T) []row.Table {
	t.Helper()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	var out []row.Table
	for _, def := range reg.Tables() {
		for _, rel := range def.Relations {
			if rel.Kind == schema.ManyToMany && rel.Name == schema.RelTags && rel.Field == schema.FieldTagIDs {
				out = append(out, def.Name)
			}
		}
	}
	if len(out) < 4 {
		t.Fatalf("only %d tagged tables: %v", len(out), out)
	}
	return out
}

// TestTagAddMatchesV017: the id, the human line and the --json envelope.
func TestTagAddMatchesV017(t *testing.T) {
	h := opstest.Bare(t)
	h.SeedProjectID(v017Project)
	res := h.Must("tag.add", `{"name":"cert_rotation"}`)
	if got := h.ID(res); got != v017Tag {
		t.Fatalf("tag id %s, v0.1.17 wrote %s", got, v017Tag)
	}
	h.Must("tag.add", `{"name":"other","color":"#ff0000"}`)
	var env struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Count         int    `json:"count"`
		Data          []map[string]json.RawMessage
	}
	list := h.Must("tag.list", ``)
	if err := json.Unmarshal([]byte(h.JSON(list)), &env); err != nil {
		t.Fatal(err)
	}
	if env.SchemaVersion != 1 || env.Kind != "tag.list" || env.Count != 2 || len(env.Data) != 2 {
		t.Fatalf("envelope %+v", env)
	}
	if _, ok := env.Data[0]["color"]; ok {
		t.Fatalf("color printed for a tag without one: %s", h.JSON(list))
	}
	if string(env.Data[1]["color"]) != `"#ff0000"` || string(env.Data[0]["project_id"]) != `"`+v017Project+`"` {
		t.Fatalf("data %s", h.JSON(list))
	}
	// v0.1.17: "cert_rotation        tag_…  (0 uses)" (name padded to 20).
	want := "cert_rotation        " + v017Tag + "  (0 uses)\nother                tag_ID  (0 uses)\n"
	if got := opstest.IDPattern(opstest.Text(list)); got != opstest.IDPattern(want) {
		t.Fatalf("list text\n got %q\nwant %q", got, want)
	}
}

// TestTagAddRefusals: names must be snake_case (dashes, capitals, spaces,
// unicode letters are E_INVALID_IDENTIFIER naming the snake_case form),
// empty is refused, and a second tag of the same name is refused instead
// of minting a twin (v0.1.17 refused too, as an E_INTERNAL constraint
// error).
func TestTagAddRefusals(t *testing.T) {
	h := opstest.New(t)
	for name, suggest := range map[string]string{
		"Cert-Rotation": "cert_rotation",
		"two words":     "two_words",
		"dash-name":     "dash_name",
		"ünïcode":       "n_code",
		"x.y":           "x_y",
	} {
		msg := h.Fails("tag.add", `{"name":`+opstest.Quote(name)+`}`, errs.InvalidIdentifier)
		if !strings.Contains(msg, "Use: "+suggest) {
			t.Errorf("%q: %s (want the suggestion %s)", name, msg, suggest)
		}
	}
	h.Fails("tag.add", `{"name":""}`, errs.InvalidInput) // required field
	h.Fails("tag.add", `{"name":"   "}`, errs.InvalidIdentifier)
	h.Must("tag.add", `{"name":"ok_1"}`)
	if _, err := h.Run("tag.add", `{"name":"ok_1"}`); err == nil {
		t.Fatal("duplicate tag accepted")
	}
	if got := opstest.Text(h.Must("tag.list", ``)); strings.Count(got, "ok_1") != 1 {
		t.Fatalf("duplicate listed: %q", got)
	}
	// a huge but valid name is kept whole
	long := strings.Repeat("a", 300)
	if got := h.Field(h.Must("tag.add", `{"name":"`+long+`"}`), "name"); got != long {
		t.Fatalf("long name cut: %d bytes", len(got))
	}
}

// TestTagAttachDetachEveryTable: attach / detach work on every table that
// carries tags; attach twice is one binding (v0.1.17 failed with a
// constraint error; the core answers the same line), detach twice
// detaches 0; the tag's use count follows.
func TestTagAttachDetachEveryTable(t *testing.T) {
	h := opstest.New(t)
	h.Must("tag.add", `{"name":"cert_rotation"}`)
	tables := tagTables(t)
	for _, tbl := range tables {
		t.Run(string(tbl), func(t *testing.T) {
			id := h.Project // a second project row would make the repo ambiguous
			if tbl != schema.TableProjects {
				id = h.Seed(tbl, nil)
			}
			in := `{"on_table":"` + string(tbl) + `","on_id":"` + id + `","tag":"cert_rotation"}`
			want := `✓ tag "cert_rotation" attached to ` + string(tbl) + "/" + id + "\n"
			for range 2 {
				if got := opstest.Text(h.Must("tag.attach", in)); got != want {
					t.Fatalf("attach text %q, want %q", got, want)
				}
			}
			if got := opstest.Text(h.Must("tag.detach", in)); got != "✓ detached 1 tag binding(s)\n" {
				t.Fatalf("detach %q", got)
			}
			if got := opstest.Text(h.Must("tag.detach", in)); got != "✓ detached 0 tag binding(s)\n" {
				t.Fatalf("second detach %q", got)
			}
			h.Must("tag.attach", in) // left attached for the count below
		})
	}
	// every seeded row is live with no closing status: all uses active.
	got := opstest.IDPattern(opstest.Text(h.Must("tag.list", ``)))
	if want := fmt.Sprintf("cert_rotation        tag_ID  (%d uses)\n", len(tables)); got != want {
		t.Fatalf("list %q, want %q", got, want)
	}
}

// TestTagUseCounts: a binding on a done or cancelled row counts in the
// total but not as active, as v0.1.17 printed "(1 active / 2 total)".
func TestTagUseCounts(t *testing.T) {
	h := opstest.New(t)
	h.Must("tag.add", `{"name":"cert_rotation"}`)
	h.Must("tag.add", `{"name":"unused"}`)
	open := h.Seed(schema.TableTasks, nil)
	done := h.Seed(schema.TableTasks, map[string]string{"status": `"done"`})
	cancelled := h.Seed(schema.TableTasks, map[string]string{"status": `"cancelled"`})
	for _, id := range []string{open, done, cancelled} {
		h.Must("tag.attach", `{"on_table":"tasks","on_id":"`+id+`","tag":"cert_rotation"}`)
	}
	got := opstest.IDPattern(opstest.Text(h.Must("tag.list", ``)))
	want := "cert_rotation        tag_ID  (1 active / 3 total)\nunused               tag_ID  (0 uses)\n"
	if got != want {
		t.Fatalf("counts\n got %q\nwant %q", got, want)
	}
}

// TestTagBindRefusals: unknown tag (v0.1.17's words and hint), a table
// that does not carry tags, a malformed id or one of another table, a
// missing or deleted row.
func TestTagBindRefusals(t *testing.T) {
	h := opstest.New(t)
	h.Must("tag.add", `{"name":"other"}`)
	task := h.Seed(schema.TableTasks, nil)
	msg := h.Fails("tag.attach", `{"on_table":"tasks","on_id":"`+task+`","tag":"nope"}`, errs.NotFound)
	if !strings.Contains(msg, `tag "nope" not found in project; create with `+"`lore tag add --name=nope`") {
		t.Fatalf("unknown tag: %s", msg)
	}
	h.Fails("tag.attach", `{"on_table":"bogus","on_id":"`+task+`","tag":"other"}`, errs.Unsupported)
	h.Fails("tag.attach", `{"on_table":"Bad Name","on_id":"`+task+`","tag":"other"}`, errs.InvalidInput)
	h.Fails("tag.attach", `{"on_table":"tasks","on_id":"tsk_bad","tag":"other"}`, errs.NotFound)
	mem := h.Seed(schema.TableMemories, nil)
	h.Fails("tag.attach", `{"on_table":"tasks","on_id":"`+mem+`","tag":"other"}`, errs.NotFound)
	h.Fails("tag.attach", `{"on_table":"tasks","on_id":"tsk_01a1a67b1b617dc7acc5f1af36e0e000","tag":"other"}`, errs.NotFound)
	h.MustYes("task.delete", `{"id":"`+task+`"}`)
	if msg := h.Fails("tag.attach", `{"on_table":"tasks","on_id":"`+task+`","tag":"other"}`, errs.NotFound); !strings.Contains(msg, "deleted") {
		t.Fatalf("deleted row: %s", msg)
	}
}

// TestTagUsesReadFromFiles: tag_ids written by one process are read back
// from the row files by a fresh App (a new process: no in-memory row) as
// the same list, so use counts, detach and a second attach still see the
// binding. Guards against a decoder handing back another value kind for a
// list field (the ops/task blocked_by bug, 1ed65b7).
func TestTagUsesReadFromFiles(t *testing.T) {
	h := opstest.New(t)
	h.Must("tag.add", `{"name":"cert_rotation"}`)
	mem := h.Seed(schema.TableMemories, nil)
	in := `{"on_table":"memories","on_id":"` + mem + `","tag":"cert_rotation"}`
	h.Must("tag.attach", in)

	fresh := h.ReopenCold()
	if got := opstest.IDPattern(opstest.Text(fresh.Must("tag.list", ``))); got != "cert_rotation        tag_ID  (1 uses)\n" {
		t.Fatalf("fresh process counts %q", got)
	}
	if !strings.Contains(fresh.JSON(fresh.Must("memory.list", `{"tags":["cert_rotation"]}`)), mem) {
		t.Fatal("memory list --tag misses a file-read row")
	}
	fresh.Must("tag.attach", in) // already bound: no second id
	if got := opstest.Text(fresh.Must("tag.detach", in)); got != "✓ detached 1 tag binding(s)\n" {
		t.Fatalf("fresh detach %q", got)
	}
}
