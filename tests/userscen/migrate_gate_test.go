//go:build userscen

package userscen

// migrate_gate_test.go — the format gate (a project written by a newer lore
// than the one a user runs) and `lore migrate enable`, as a user meets them:
//
//	TestMigrateGateNewerRow        a row file written by a newer lore (_v 3): show is
//	                               E_OLD_LORE exit 19 with the exact message and
//	                               `lore upgrade`; list leaves it out and says so; migrate
//	                               status counts it as newer; migrate run and other writes
//	                               leave its bytes alone; doctor names it
//	TestMigrateGateDeclaredMatrix  _meta.json declares more than this lore knows (a floor
//	                               above it, an unknown feature, both, unreadable JSON, a
//	                               floor that is not a string) × every write noun: each
//	                               write is refused with the exact code, exit, message and
//	                               next step (human and --json), reads keep working,
//	                               _meta.json is never rewritten (migrate run included); restoring it from git
//	                               makes writes work again; `lore api` refuses the same
//	TestMigrateEnable              enable with no feature, an unknown one (the hint lists
//	                               the known ones), a new one (added, sorted, floor kept)
//	                               and the same one again (added false), human and --json

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// migMetaPath is the project's format declaration.
const migMetaPath = migDataDir + "/_meta.json"

// migFutureVersion is a row format above what this lore reads (FormatVersion 2).
const migFutureVersion = 3

// migHTTPGuard is the HTTP status of the guard class (E_OLD_LORE, model/errs).
const migHTTPGuard = 403

// migBinaryVersion is what the binary under test calls itself (`lore
// version`'s first line, "lore <version>"); the gate's messages name it.
func migBinaryVersion(t *testing.T, c *th.Clone) string {
	t.Helper()
	first, _, _ := strings.Cut(c.Lore("version").OK(t).Stdout, "\n")
	v, ok := strings.CutPrefix(strings.TrimSpace(first), "lore ")
	if !ok || v == "" {
		t.Fatalf("lore version: unexpected first line %q", first)
	}
	return v
}

func TestMigrateGateNewerRow(t *testing.T) {
	scenParallel(t)
	_, _, c := migNew(t)
	c.Lore("memory", "add", "an ordinary note", "--no-live").OK(t)
	id := "mem_01a1a59b891c70b78914000000000077"
	doc := `{"_hlc":"2026-11-04T08:09:56.252Z/0000/act_0e45506e64357c94a87e70ef1c6bbba9","_table":"memories","_v":` + strconv.Itoa(migFutureVersion) +
		`,"body":"from the future","created_at":"2026-11-04T08:09:56.252002659Z","id":"` + id +
		`","kind":"fact","new_field":1,"source_kind":"manual","updated_at":"2026-11-04T08:09:56.252002894Z","updated_by":"act_0e45506e64357c94a87e70ef1c6bbba9"}`
	rel := migDataDir + "/memories/" + id + ".json"
	c.WriteFile(rel, doc)
	const msg = "row: written by a newer lore (_v 3, this lore reads up to 2)"

	c.Lore("memory", "show", id).Fails(t, th.ExitGuard, "[E_OLD_LORE] "+msg, "hint: upgrade lore: lore upgrade")
	r := c.Lore("memory", "show", id, "--json")
	if e := restCLIErr(r); r.Code != th.ExitGuard || e == nil || e.Code != "E_OLD_LORE" || e.Message != msg || e.Exit != th.ExitGuard || e.Class != "guard" || e.Hint != "upgrade lore: lore upgrade" {
		t.Fatalf("show --json: exit %d envelope %+v\n%s", r.Code, e, r)
	}

	list := c.Lore("memory", "list").OK(t)
	list.Says(t, "an ordinary note", "invalid-file", rel, msg, "left out of every read (lore doctor)").Never(t, "from the future")

	if p := migRepoPlace(t, migStatus(t, c)); p.Newer != 1 || p.Total != p.V1+p.V2+p.Newer {
		t.Fatalf("migrate status: %+v, want newer 1 counted in the total", p)
	}
	c.Lore("migrate", "run", "--no-push").OK(t)
	c.Lore("memory", "add", "another note", "--no-live").OK(t)
	if got, _ := c.ReadFile(rel); got != doc {
		t.Fatalf("a row written by a newer lore was rewritten:\n%s", got)
	}
	c.Lore("doctor").Says(t, rel, "is not a usable row file", msg)
}

// migGateCase is one way _meta.json can declare more than this lore knows.
type migGateCase struct {
	name string
	// edit changes the decoded _meta.json; nil means raw replaces the file.
	edit func(m map[string]json.RawMessage)
	raw  string
	// msg and hint are the refusal (bin is the binary's version).
	msg  func(bin string) string
	hint string
}

func migGateCases() []migGateCase {
	const tail = ". Reading works; writing is off until you upgrade."
	const metaHint = "restore .lore/data/_meta.json from git history (git log -p -- .lore/data/_meta.json), then retry; lore doctor shows the features the files need"
	addFeature := func(m map[string]json.RawMessage) {
		var fs []string
		_ = json.Unmarshal(m["features"], &fs) // a fresh project declares a list
		b, _ := json.Marshal(append(fs, "zz_future"))
		m["features"] = b
	}
	return []migGateCase{
		{name: "floor above this lore", edit: func(m map[string]json.RawMessage) { m["min_lore_version"] = json.RawMessage(`"9.9.9"`) },
			msg: func(bin string) string {
				return "this project needs lore 9.9.9 or later to write (this is " + bin + ")" + tail
			}, hint: "lore upgrade"},
		{name: "unknown feature", edit: addFeature,
			msg: func(string) string {
				return "this project uses a lore feature this binary does not know: zz_future" + tail
			},
			hint: "lore upgrade"},
		{name: "unknown feature and floor", edit: func(m map[string]json.RawMessage) {
			addFeature(m)
			m["min_lore_version"] = json.RawMessage(`"9.9.9"`)
		}, msg: func(bin string) string {
			return "this project uses a lore feature this binary does not know: zz_future (needs lore 9.9.9 or later; this is " + bin + ")" + tail
		}, hint: "lore upgrade"},
		{name: "unreadable JSON", raw: "{broken",
			msg: func(string) string {
				return "this project's .lore/data/_meta.json cannot be read (not a JSON object); lore is read-only here until it is fixed"
			},
			hint: metaHint},
		{name: "floor not a string", edit: func(m map[string]json.RawMessage) { m["min_lore_version"] = json.RawMessage(`18`) },
			msg: func(string) string {
				return "this project's .lore/data/_meta.json cannot be read (min_lore_version is not a string); lore is read-only here until it is fixed"
			}, hint: metaHint},
	}
}

// migWrites are writes of every noun a gate must stop.
var migWrites = [][]string{
	{"memory", "add", "gate note", "--no-live"},
	{"rule", "add", "gate rule", "--no-live"},
	{"decision", "add", "gate decision", "--title", "gate"},
	{"task", "add", "gate task", "--no-live"},
	{"pattern", "add", "gate pattern", "--title", "gate"},
	// migrate run rewrites files outside the engine's Update, yet it is a
	// write like the others (INTEG-MIGREST-1).
	{"migrate", "run"},
}

// migReads must keep working behind the gate.
var migReads = [][]string{{"memory", "list"}, {"rule", "list"}, {"task", "list"}, {"migrate", "status"}}

func TestMigrateGateDeclaredMatrix(t *testing.T) {
	scenParallel(t) // its sub-tests each build their own World and run in parallel
	for _, gc := range migGateCases() {
		t.Run(gc.name, func(t *testing.T) {
			scenParallel(t)
			_, _, c := migNew(t)
			c.Lore("memory", "add", "written before the gate", "--no-live").OK(t)
			c.CommitAll("a note")
			bin := migBinaryVersion(t, c)
			orig, ok := c.ReadFile(migMetaPath)
			if !ok {
				t.Fatal("a fresh project has no _meta.json")
			}
			gated := gc.raw
			if gc.edit != nil {
				m := migDoc(t, []byte(orig))
				gc.edit(m)
				gated = string(migEncodeV017(t, m))
			}
			c.WriteFile(migMetaPath, gated)
			msg := gc.msg(bin)

			for _, w := range migWrites {
				r := c.Lore(w...).Fails(t, th.ExitGuard, "[E_OLD_LORE] "+msg, "hint: "+gc.hint)
				if strings.TrimSpace(c.Status()) != "M "+migMetaPath {
					t.Fatalf("%v refused, yet the work tree changed:\n%s\n%s", w, c.Status(), r)
				}
				jr := c.Lore(append(slices.Clone(w), "--json")...)
				e := restCLIErr(jr)
				if jr.Code != th.ExitGuard || e == nil || e.Code != "E_OLD_LORE" || e.Message != msg || e.Hint != gc.hint || e.Exit != th.ExitGuard || e.Class != "guard" {
					t.Fatalf("%v --json: exit %d envelope %+v\n%s", w, jr.Code, e, jr)
				}
			}
			for _, rd := range migReads {
				c.Lore(rd...).OK(t)
			}
			c.Lore("memory", "list").Says(t, "written before the gate")
			if got, _ := c.ReadFile(migMetaPath); got != gated {
				t.Fatalf("_meta.json was rewritten behind the gate:\n%s", got)
			}

			// REST refuses the same write with the same code (guard: 403).
			srv := restStart(t, c)
			rep := srv.post(t, "/v1/memories", `{"body":"gate note over rest"}`)
			if rep.status != migHTTPGuard || rep.err == nil || rep.err.Code != "E_OLD_LORE" || rep.err.Message != msg {
				t.Fatalf("REST write behind the gate: %d %s", rep.status, rep.body)
			}
			if got := srv.get(t, "/v1/memories"); got.status != http.StatusOK {
				t.Fatalf("REST read behind the gate: %d %s", got.status, got.body)
			}

			// The next step works: restore _meta.json from git, then write.
			c.Git("checkout", "--", migMetaPath)
			c.Lore("memory", "add", "after the restore", "--no-live").OK(t)
		})
	}
}

func TestMigrateEnable(t *testing.T) {
	scenParallel(t)
	_, _, c := migNew(t)
	// No feature (or an empty one): the op's own refusal and hint, not the
	// decoder's bare "missing required field" (INTEG-MIGREST-2).
	for _, args := range [][]string{{"migrate", "enable"}, {"migrate", "enable", ""}} {
		c.Lore(args...).Fails(t, migExitInvalid, "[E_INVALID_INPUT] migrate: name the feature to enable", "hint: lore migrate enable --feature <feature>")
	}
	c.Lore("migrate", "enable", "nope").Fails(t, migExitInvalid, "E_INVALID_INPUT", `migrate: "nope" is not a feature this lore knows`,
		"hint: known features: live, signals, hlc, meta, trash, task_tree, time, plan_status, policy, resources, refs, vars, personal_flat")

	type enableOut struct {
		Feature        string   `json:"feature"`
		Added          bool     `json:"added"`
		Features       []string `json:"features"`
		MinLoreVersion string   `json:"min_lore_version"`
	}
	before := migStatus(t, c).Features
	if slices.Contains(before, "vars") {
		t.Fatalf("a fresh project already declares vars: %v", before)
	}
	first := th.DecodeJSON[enableOut](t, c.Lore("migrate", "enable", "vars", "--json"))
	if first.Feature != "vars" || !first.Added || !slices.Contains(first.Features, "vars") || !slices.IsSorted(first.Features) || first.MinLoreVersion != "0.1.18" {
		t.Fatalf("first enable: %+v", first)
	}
	for _, f := range before {
		if !slices.Contains(first.Features, f) {
			t.Fatalf("enable dropped feature %s: %v", f, first.Features)
		}
	}
	meta, _ := c.ReadFile(migMetaPath)
	again := th.DecodeJSON[enableOut](t, c.Lore("migrate", "enable", "vars", "--json"))
	if again.Added || !slices.Equal(again.Features, first.Features) {
		t.Fatalf("second enable: %+v, want added false and the same features", again)
	}
	if now, _ := c.ReadFile(migMetaPath); now != meta {
		t.Fatalf("enabling a declared feature rewrote _meta.json:\n%s", now)
	}
	c.Lore("migrate", "enable", "vars").OK(t).Says(t, "feature: vars", "features: ").Never(t, "added: true")
	if st := migStatus(t, c); !slices.Equal(st.Features, first.Features) {
		t.Fatalf("status features %v, want %v", st.Features, first.Features)
	}
}

// TestMigrateRunGated (INTEG-MIGREST-1): in a project whose _meta.json
// floor is above this lore, a planted v0.1.17 row is left as it is:
// `lore migrate run` is refused like every other write (E_OLD_LORE, exit
// 19, `lore upgrade`, human and --json) and changes no file, while the dry
// run, a read, still lists the row it would rewrite. The clone has no
// remote: with one, the team lock's signals write was already gated, so
// the hole was only in a repo with no remote origin.
func TestMigrateRunGated(t *testing.T) {
	scenParallel(t)
	_, _, c := migNew(t)
	c.Git("remote", "remove", "origin")
	k := migKinds()[0]
	id := migAdd(t, c, k)
	path := migRowPath(c, k.table, id)
	v1 := migDowngrade(t, path)
	c.CommitAll("a v0.1.17 row")
	orig, _ := c.ReadFile(migMetaPath)
	m := migDoc(t, []byte(orig))
	m["min_lore_version"] = json.RawMessage(`"9.9.9"`)
	c.WriteFile(migMetaPath, string(migEncodeV017(t, m)))
	msg := "this project needs lore 9.9.9 or later to write (this is " + migBinaryVersion(t, c) + "). Reading works; writing is off until you upgrade."

	c.Lore("migrate", "run").Fails(t, th.ExitGuard, "[E_OLD_LORE] "+msg, "hint: lore upgrade")
	jr := c.Lore("migrate", "run", "--json")
	if e := restCLIErr(jr); jr.Code != th.ExitGuard || e == nil || e.Code != "E_OLD_LORE" || e.Message != msg {
		t.Fatalf("migrate run --json behind the gate: exit %d\n%s", jr.Code, jr)
	}
	if got := migRead(t, path); !bytes.Equal(got, v1) {
		t.Fatalf("the gated run rewrote the v0.1.17 row:\n%s", got)
	}
	if st := strings.TrimSpace(c.Status()); st != "M "+migMetaPath {
		t.Fatalf("the gated run changed the work tree:\n%s", st)
	}
	// C2: the dry run summarises per table, naming the row ids
	c.Lore("migrate", "run", "--dry-run").OK(t).Says(t, k.table+": 1 row ("+id+")")
}
