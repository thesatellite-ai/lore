package settings_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// mustAll runs each op with --yes and fails on the first error.
func mustAll(t *testing.T, h *opstest.H, steps ...[2]string) {
	t.Helper()
	for _, s := range steps {
		h.MustYes(s[0], s[1])
	}
}

// refusal is one input that must fail with code; msg is part of the
// message ("" = any).
type refusal struct {
	op, in string
	code   errs.Code
	msg    string
}

func checkRefusals(t *testing.T, h *opstest.H, cases []refusal) {
	t.Helper()
	for _, c := range cases {
		_, err := h.RunYes(c.op, c.in)
		if !errs.Has(err, c.code) || !strings.Contains(errString(err), c.msg) {
			t.Errorf("%s %s: %v, want %s %q", c.op, c.in, err, c.code, c.msg)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestKindsAndFields: resource kinds and their fields, add / edit /
// rename / remove, with every refusal (snake_case, closed sets, taken
// names, ref without ref-kind, tri-state flags, in-use values).
func TestKindsAndFields(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	add := h.MustYes(settings.OpKindAdd, `{"kind":"server","description":"a box","name_format":"dns"}`)
	if got := opstest.IDPattern(opstest.Text(add)); got != "added kind knd_…"+h.ID(add)[len(h.ID(add))-4:]+" server in this repo\n" {
		t.Errorf("kind add text %q", got)
	}
	mustAll(t, h,
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"ip","type":"ip","required":true}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"env","type":"text","values":"prod, staging,,"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"pw","type":"text","sensitive":true}`},
		[2]string{settings.OpKindAdd, `{"kind":"db"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"db","type":"ref","ref_kind":"db"}`},
	)
	show := h.Must(settings.OpKindShow, `{"id":"server"}`)
	want := h.ID(add) + "  server  (repo)\n  a box\n  db  ref→db\n  env  text  values prod, staging\n  ip  ip  required\n  pw  text  sensitive\n"
	if got := opstest.Text(show); got != want {
		t.Errorf("kind show:\n%q\nwant\n%q", got, want)
	}
	checkRefusals(t, h, []refusal{
		{settings.OpKindAdd, `{"kind":"server"}`, errs.NameTaken, `name "server" already exists in repo`},
		{settings.OpKindAdd, `{"kind":"Bad-Kind"}`, errs.ResourceName, "is not snake_case"},
		{settings.OpKindAdd, `{"kind":"9lives"}`, errs.ResourceName, "is not snake_case"},
		{settings.OpKindAdd, `{"kind":"ünï"}`, errs.ResourceName, "is not snake_case"},
		{settings.OpKindAdd, `{"kind":""}`, errs.InvalidInput, ""},
		{settings.OpKindAdd, `{"kind":"vm","name_format":"zzz"}`, errs.InvalidInput, "--name-format \"zzz\" is not one of id, dns, ip, text"},
		{settings.OpKindEdit, `{"id":"server"}`, errs.InvalidInput, "nothing to change"},
		{settings.OpKindEdit, `{"id":"nope","description":"x"}`, errs.KindUnknown, `no resource kind "nope"`},
		{settings.OpKindRename, `{"id":"server","new_name":"db"}`, errs.NameTaken, `a kind named "db" already exists`},
		{settings.OpFieldAdd, `{"kind":"server","field":"ip","type":"ip"}`, errs.InvalidInput, `already has a field "ip"`},
		{settings.OpFieldAdd, `{"kind":"server","field":"x","type":"nope"}`, errs.InvalidInput, `--type "nope" is not a value type`},
		{settings.OpFieldAdd, `{"kind":"server","field":"r","type":"ref"}`, errs.InvalidInput, "--type ref needs --ref-kind"},
		{settings.OpFieldAdd, `{"kind":"server","field":"r","type":"text","ref_kind":"db"}`, errs.InvalidInput, "--ref-kind needs --type ref"},
		{settings.OpFieldAdd, `{"kind":"server","field":"Bad","type":"text"}`, errs.ResourceName, "is not snake_case"},
		{settings.OpFieldAdd, `{"kind":"nope","field":"x","type":"text"}`, errs.KindUnknown, ""},
		{settings.OpFieldEdit, `{"kind":"server","field":"ip","required":true,"no_required":true}`, errs.InvalidInput, "give --required or --no-required, not both"},
		{settings.OpFieldEdit, `{"kind":"server","field":"ip"}`, errs.InvalidInput, "nothing to change"},
		{settings.OpFieldEdit, `{"kind":"server","field":"nope","required":true}`, errs.NotFound, `kind server has no field "nope"`},
		{settings.OpFieldRename, `{"kind":"server","field":"ip","new_name":"env"}`, errs.InvalidInput, `already has a field "env"`},
	})
	// rename keeps the id; the old name is gone, the new one resolves.
	ren := h.MustYes(settings.OpKindRename, `{"id":"server","new_name":"host"}`)
	if h.ID(ren) != h.ID(add) || !strings.HasPrefix(opstest.Text(ren), "✓ renamed kind server → host; ids unchanged") {
		t.Errorf("rename %s %q", h.ID(ren), opstest.Text(ren))
	}
	h.Fails(settings.OpKindShow, `{"id":"server"}`, errs.KindUnknown)
	h.Must(settings.OpKindShow, `{"id":"`+h.ID(add)+`"}`)
	// the kinds table lists fields by name.
	if got := opstest.IDPattern(opstest.Text(h.Must(settings.OpKinds, ``))); got != "ID\tKIND\tSTORE\tROWS\tFIELDS\nknd_ID\thost\trepo\t0\tdb, env, ip, pw\nknd_ID\tdb\trepo\t0\t\n" &&
		got != "ID\tKIND\tSTORE\tROWS\tFIELDS\nknd_ID\tdb\trepo\t0\t\nknd_ID\thost\trepo\t0\tdb, env, ip, pw\n" {
		t.Errorf("kinds %q", got)
	}
}

// TestFieldValuesInUse: a closed-set value or a field holding values on
// resources is refused while in use, removable with --drop.
func TestFieldValuesInUse(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"server","name_format":"dns"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"env","type":"text","values":"prod,staging"}`},
		[2]string{settings.OpResAdd, `{"kind":"server","name":"web1.example.com","set":["env=prod"]}`},
	)
	checkRefusals(t, h, []refusal{
		{settings.OpFieldEdit, `{"kind":"server","field":"env","remove_values":["prod"]}`, errs.ResourceInUse, `server.env value "prod" is used by 1 resources`},
		{settings.OpFieldRemove, `{"kind":"server","field":"env"}`, errs.ResourceInUse, "server.env has values on 1 rows"},
	})
	// an unused value goes; a new one comes.
	h.MustYes(settings.OpFieldEdit, `{"kind":"server","field":"env","remove_values":["staging"],"add_values":["dev"]}`)
	h.MustYes(settings.OpResEdit, `{"id":"web1.example.com","set":["env=dev"]}`)
	h.Fails(settings.OpResEdit, `{"id":"web1.example.com","set":["env=staging"]}`, errs.ResourceInvalid)
	rm := h.MustYes(settings.OpFieldRemove, `{"kind":"server","field":"env","drop":true}`)
	if !strings.Contains(opstest.Text(rm), "values dropped from 1 rows") {
		t.Errorf("drop text %q", opstest.Text(rm))
	}
	if v := resValues(t, h, "web1.example.com"); len(v) != 0 {
		t.Errorf("value kept after --drop: %v", v)
	}
}

// TestResourcesMatrix: add / edit / show / list --where / check / stale /
// archive / delete, every value-type and name-format refusal, ref fields
// resolving by name, sensitive values masked unless confirmed.
func TestResourcesMatrix(t *testing.T) {
	t.Parallel()
	h := multiStore(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"server","name_format":"dns"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"ip","type":"ip","required":true}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"env","type":"text","values":"prod,staging"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"pw","type":"text","sensitive":true}`},
		[2]string{settings.OpKindAdd, `{"kind":"db"}`},
		[2]string{settings.OpKindAdd, `{"kind":"app"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"app","field":"db","type":"ref","ref_kind":"db"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"app","field":"port","type":"number"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"app","field":"tags","type":"list"}`},
		[2]string{settings.OpResAdd, `{"kind":"db","name":"main_db"}`},
	)
	created := h.MustYes(settings.OpResAdd, `{"kind":"server","name":"web1.example.com","set":["ip=10.0.0.1","env=prod","pw=hunter2"]}`)
	if got := opstest.IDPattern(opstest.Text(created)); got != "created res_…"+h.ID(created)[len(h.ID(created))-4:]+"  server  repo:server/web1.example.com\n  env = prod; ip = 10.0.0.1; pw = •••\n" {
		t.Errorf("add text %q", got)
	}
	checkRefusals(t, h, []refusal{
		{settings.OpResAdd, `{"kind":"server","name":"web2.example.com","set":["env=prod"]}`, errs.ResourceInvalid, "required field ip is missing"},
		{settings.OpResAdd, `{"kind":"server","name":"web3.example.com","set":["ip=10.0.0.3","env=dev"]}`, errs.ResourceInvalid, `env "dev" is not one of prod, staging`},
		{settings.OpResAdd, `{"kind":"server","name":"web4.example.com","set":["ip=999.1.1.1"]}`, errs.ResourceInvalid, ""},
		{settings.OpResAdd, `{"kind":"server","name":"web1.example.com","set":["ip=10.0.0.9"]}`, errs.ResourceTaken, "server web1.example.com already exists in repo"},
		{settings.OpResAdd, `{"kind":"server","name":"not a host","set":["ip=10.0.0.9"]}`, errs.ResourceName, ""},
		{settings.OpResAdd, `{"kind":"db","name":"Main DB"}`, errs.ResourceName, "the name does not fit the kind's name format"},
		{settings.OpResAdd, `{"kind":"app","name":"api2","set":["db=nope"]}`, errs.ResourceInvalid, `no resources row named "nope"`},
		{settings.OpResAdd, `{"kind":"app","name":"api3","set":["port=abc"]}`, errs.ResourceInvalid, `port "abc" is not a number`},
		{settings.OpResAdd, `{"kind":"app","name":"api4","set":["zz=1"]}`, errs.ResourceInvalid, "unknown field zz (fields: db, port, tags)"},
		{settings.OpResAdd, `{"kind":"app","name":"api5","set":["port="]}`, errs.InvalidInput, "an empty value is refused"},
		{settings.OpResAdd, `{"kind":"app","name":"api6","set":["port"]}`, errs.InvalidInput, "want field=value"},
		{settings.OpResAdd, `{"kind":"nope","name":"x"}`, errs.KindUnknown, ""},
		{settings.OpResEdit, `{"id":"web1.example.com"}`, errs.InvalidInput, "nothing to change"},
		{settings.OpResEdit, `{"id":"web1.example.com","unset":["ip"]}`, errs.ResourceInvalid, "required field ip is missing"},
		{settings.OpResList, `{"kind":"server","where":["env"]}`, errs.InvalidInput, "no operator; want <key><op><value>"}, // XQ7: the one filter grammar words it
		{settings.OpResShow, `{"id":"nope"}`, errs.NotFound, ""},
	})
	// a ref field resolves by name and is stored as the row id; a list
	// field takes a comma list or a JSON array.
	api := h.MustYes(settings.OpResAdd, `{"kind":"app","name":"api","set":["db=main_db","port=8080","tags=a, b"]}`)
	h.MustYes(settings.OpResAdd, `{"kind":"app","name":"api_web","to":"kb/web","set":["tags=[\"x\"]"]}`)
	if v := resValues(t, h, "api"); v["db"] != "main_db" || v["port"] != "8080" {
		t.Errorf("api values %v", v)
	}
	// list --where (all must match), --archived, the store label.
	list := func(in string) []settings.Resource {
		var out settings.ResListOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpResList, in))), &out); err != nil {
			t.Fatal(err)
		}
		return out.Data
	}
	// The harness repo is a client repo (no RepoIsClient hook: fail
	// closed), so api_web in the store is left out (RES 3.9).
	if got := list(`{"kind":"app"}`); len(got) != 1 || got[0].ID != h.ID(api) {
		t.Errorf("app list %+v", got)
	}
	if got := list(`{"kind":"app","where":["port=8080"]}`); len(got) != 1 || got[0].ID != h.ID(api) {
		t.Errorf("--where %+v", got)
	}
	if got := list(`{"kind":"app","where":["port=8080","db=nope"]}`); len(got) != 0 {
		t.Errorf("--where AND %+v", got)
	}
	// sensitive: masked; show_sensitive asks first.
	if v := resValues(t, h, "web1.example.com"); v["pw"] != "•••" {
		t.Errorf("sensitive shown without asking: %v", v)
	}
	h.Fails(settings.OpResShow, `{"id":"web1.example.com","show_sensitive":true}`, errs.NeedsConfirm)
	h.Fails(settings.OpResList, `{"kind":"server","show_sensitive":true}`, errs.NeedsConfirm)
	if got := opstest.Text(h.MustYes(settings.OpResShow, `{"id":"web1.example.com","show_sensitive":true}`)); !strings.Contains(got, "pw = hunter2") {
		t.Errorf("confirmed show %q", got)
	}
	// check stamps last_checked (not stale); archive hides from list.
	h.MustYes(settings.OpResCheck, `{"id":"web1.example.com"}`)
	var stale settings.ResListOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpResStale, ``))), &stale); err != nil {
		t.Fatal(err)
	}
	for _, r := range stale.Data {
		if r.Name == "web1.example.com" {
			t.Errorf("just-checked resource listed stale")
		}
	}
	h.MustYes(settings.OpResArchive, `{"id":"web1.example.com"}`)
	if got := list(`{"kind":"server"}`); len(got) != 0 {
		t.Errorf("archived resource listed: %+v", got)
	}
	if got := list(`{"kind":"server","archived":true}`); len(got) != 1 || got[0].ArchivedAt == "" {
		t.Errorf("--archived %+v", got)
	}
	// delete asks, warns about dangling refs, keeps a tombstone; the name
	// can be created again under a fresh id.
	dbID := resID(t, h, "main_db")
	h.Fails(settings.OpResDelete, `{"id":"main_db"}`, errs.NeedsConfirm)
	del := h.MustYes(settings.OpResDelete, `{"id":"main_db"}`)
	if !strings.Contains(opstest.Text(del), "still pointing at it: api") {
		t.Errorf("delete text %q", opstest.Text(del))
	}
	if v := resValues(t, h, "api"); v["db"] != dbID {
		t.Errorf("dangling ref shows %q, want the old id %s", v["db"], dbID)
	}
	h.Fails(settings.OpResShow, `{"id":"main_db"}`, errs.InTrash) // DELETION 3.2
	again := h.ID(h.MustYes(settings.OpResAdd, `{"kind":"db","name":"main_db"}`))
	if again == dbID {
		t.Errorf("re-created resource reused the tombstoned id")
	}
}

// resource returns resource.show's data for name.
func resource(t *testing.T, h *opstest.H, name string) settings.Resource {
	t.Helper()
	var out settings.ResOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpResShow, `{"id":`+opstest.Quote(name)+`}`))), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data
}

func resValues(t *testing.T, h *opstest.H, name string) map[string]string {
	t.Helper()
	return resource(t, h, name).Values
}

func resID(t *testing.T, h *opstest.H, name string) string {
	t.Helper()
	return resource(t, h, name).ID
}

// TestVarsMatrix: set / update / rename (alias) / rm / where, nearest
// wins across places, type checks, and an update keeping what it was not
// told to change.
func TestVarsMatrix(t *testing.T) {
	t.Parallel()
	h := multiStore(t)
	add := h.MustYes(settings.OpVarsSet, `{"name":"api_url","value":"https://x.example","type":"url"}`)
	if got := opstest.Text(add); got != "Added var_…"+h.ID(add)[len(h.ID(add))-4:]+" api_url = https://x.example (url) in repo (not shareable: clients see ${var:<name>}).\n" {
		t.Errorf("add text %q", got)
	}
	h.MustYes(settings.OpVarsSet, `{"name":"api_url","value":"https://y.example","to":"kb"}`)
	h.MustYes(settings.OpVarsSet, `{"name":"region","value":"eu","to":"kb/web"}`)
	checkRefusals(t, h, []refusal{
		{settings.OpVarsSet, `{"name":"Bad","value":"1"}`, errs.InvalidInput, `variable name "Bad" is not snake_case`},
		{settings.OpVarsSet, `{"name":"","value":"1"}`, errs.InvalidInput, ""},
		{settings.OpVarsSet, `{"name":"n","value":"abc","type":"number"}`, errs.InvalidInput, `"abc" is not a number`},
		{settings.OpVarsSet, `{"name":"n","value":"1","type":"zzz"}`, errs.InvalidInput, `--type "zzz" is not one of`},
		{settings.OpVarsSet, `{"name":"n","value":"x","type":"ip"}`, errs.InvalidInput, ""},
		{settings.OpVarsSet, `{"name":"n","value":"x","to":"nowhere"}`, errs.NotFound, ""},
		// an update is checked against the variable's own type.
		{settings.OpVarsSet, `{"name":"api_url","value":"not a url"}`, errs.InvalidInput, `"not a url" is not a url`},
		{settings.OpVarsShow, `{"id":"nope"}`, errs.NotFound, `no variable "nope"`},
		{settings.OpVarsRename, `{"id":"api_url","new_name":"Bad"}`, errs.InvalidInput, "is not snake_case"},
		{settings.OpVarsRename, `{"id":"api_url","new_name":"api_url"}`, errs.InvalidInput, "already named api_url"},
	})
	// nearest wins: the repo definition hides kb's; --all shows both.
	want := "NAME\tVALUE\tTYPE\tWHERE\napi_url\thttps://x.example\turl\trepo\nregion\teu\ttext\tkb/web\n"
	if got := opstest.Text(h.Must(settings.OpVarsList, ``)); got != want {
		t.Errorf("list:\n%q\nwant\n%q", got, want)
	}
	if got := opstest.Text(h.Must(settings.OpVarsWhere, `{"id":"api_url"}`)); got != "NAME\tVALUE\tTYPE\tWHERE\napi_url\thttps://x.example\turl\trepo\napi_url\thttps://y.example\ttext\tkb (hidden)\n" {
		t.Errorf("where %q", got)
	}
	if got := opstest.Text(h.Must(settings.OpVarsShow, `{"id":"api_url","to":"kb"}`)); !strings.Contains(got, "api_url = https://y.example (text) in kb") {
		t.Errorf("show --to kb %q", got)
	}
	// an update keeps the type and the id.
	upd := h.MustYes(settings.OpVarsSet, `{"name":"api_url","value":"https://z.example"}`)
	if h.ID(upd) != h.ID(add) || !strings.Contains(opstest.Text(upd), "api_url = https://z.example (url) in repo") {
		t.Errorf("update %s %q", h.ID(upd), opstest.Text(upd))
	}
	if v := varOf(t, h, "api_url"); v.Type != "url" || v.Value != "https://z.example" {
		t.Errorf("after update %+v", v)
	}
	// rename keeps the old name as an alias; a real name elsewhere wins
	// over an alias.
	h.MustYes(settings.OpVarsRename, `{"id":"api_url","new_name":"base_url"}`)
	if v := varOf(t, h, "base_url"); v.ID != h.ID(add) || len(v.Aliases) != 1 || v.Aliases[0] != "api_url" {
		t.Errorf("renamed %+v", v)
	}
	if v := varOf(t, h, "api_url"); v.Store != "kb" {
		t.Errorf("api_url after rename resolves to %+v, want kb's real name", v)
	}
	h.MustYes(settings.OpVarsRm, `{"id":"base_url"}`)
	h.Fails(settings.OpVarsShow, `{"id":"base_url"}`, errs.InTrash) // DELETION 3.2
	if got := opstest.Text(h.Must(settings.OpVarsList, ``)); got != "NAME\tVALUE\tTYPE\tWHERE\napi_url\thttps://y.example\ttext\tkb\nregion\teu\ttext\tkb/web\n" {
		t.Errorf("list after rm %q", got)
	}
}

// TestVarsUpdateKeepsShareable: `vars set` on an existing shareable
// variable changes its value only; switching shareable is `lore move`
// (CONFIG_GUIDE VARS ARE DATA). It used to reset shareable to false and
// the type to text, silently changing who sees the value.
func TestVarsUpdateKeepsShareable(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpVarsSet, `{"name":"u","value":"https://a.example","type":"url","shareable":true,"description":"staging"}`)
	got := opstest.Text(h.MustYes(settings.OpVarsSet, `{"name":"u","value":"https://b.example"}`))
	if !strings.Contains(got, "(url) in repo (shareable: resolves for everyone with access).") {
		t.Errorf("update text %q", got)
	}
	if v := varOf(t, h, "u"); !v.Shareable || v.Type != "url" || v.Description != "staging" {
		t.Errorf("update changed more than the value: %+v", v)
	}
	// an explicit --type still changes the type.
	h.MustYes(settings.OpVarsSet, `{"name":"u","value":"plain","type":"text"}`)
	if v := varOf(t, h, "u"); v.Type != "text" || !v.Shareable {
		t.Errorf("explicit type %+v", v)
	}
}

func varOf(t *testing.T, h *opstest.H, name string) settings.Var {
	t.Helper()
	var out settings.VarOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpVarsShow, `{"id":`+opstest.Quote(name)+`}`))), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data
}

// TestVarsRefuseSecrets: a variable never holds a secret (D22): the
// scanner checks the value; without a yes the write stops.
func TestVarsRefuseSecrets(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	tok := "ghp_" + strings.Repeat("aB3dE6gH9", 4) // built at runtime: no literal token in source
	msg := h.Fails(settings.OpVarsSet, `{"name":"tok","value":"`+tok+`"}`, errs.NeedsConfirm)
	if strings.Contains(msg, tok) {
		t.Errorf("refusal echoes the whole secret: %s", msg)
	}
	h.Fails(settings.OpVarsShow, `{"id":"tok"}`, errs.NotFound)
}

// TestMetaMatrix: meta add / edit --values (a diff) / describe / rename /
// keys / value add / value rename / delete.
func TestMetaMatrix(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	add := h.MustYes(settings.OpMetaAdd, `{"name":"tier","type":"text","values":"gold, silver","strict":true}`)
	if got := opstest.Text(add); got != "added mky_…"+h.ID(add)[len(h.ID(add))-4:]+" tier (text, strict, values gold, silver) to this repo (not internal)\n" {
		t.Errorf("add text %q", got)
	}
	h.MustYes(settings.OpMetaAdd, `{"name":"owner","type":"text","description":"who owns it"}`)
	checkRefusals(t, h, []refusal{
		{settings.OpMetaAdd, `{"name":"owner","type":"text"}`, errs.MetaTaken, `name "owner"`},
		{settings.OpMetaAdd, `{"name":"x","type":"blob"}`, errs.MetaType, `--type "blob" is not a metadata type`},
		{settings.OpMetaAdd, `{"name":"Bad","type":"text"}`, errs.MetaKey, "is not a snake_case key name"},
		{settings.OpMetaAdd, `{"name":"d","type":"text","description":"` + strings.Repeat("é", 301) + `"}`, errs.InvalidInput, "at most 300 characters"},
		{settings.OpMetaAdd, `{"name":"v","type":"text","values":"Gold"}`, errs.InvalidInput, `allowed value "Gold" is not snake_case`},
		{settings.OpMetaAdd, `{"name":"i","type":"text","internal":true,"no_internal":true}`, errs.InvalidInput, "not both"},
		{settings.OpMetaEdit, `{"id":"tier"}`, errs.InvalidInput, "nothing to change"},
		{settings.OpMetaEdit, `{"id":"nope","strict":true}`, errs.NotFound, `no metadata key "nope"`},
		{settings.OpMetaRename, `{"id":"owner","new_name":"tier"}`, errs.MetaTaken, `a metadata key named "tier" already exists there`},
		{settings.OpMetaValueAdd, `{"key":"tier","value":"gold"}`, errs.MetaTaken, `name "gold"`},
		{settings.OpMetaValueRename, `{"key":"tier","value":"nope","new_value":"x"}`, errs.NotFound, `tier has no value "nope"`},
		{settings.OpMetaValueRename, `{"key":"tier","value":"gold","new_value":"silver"}`, errs.InvalidInput, `already has the value "silver"`},
	})
	// 300 characters (not bytes) is the limit: 300 two-byte runes pass.
	h.MustYes(settings.OpMetaDescribe, `{"id":"owner","text":"`+strings.Repeat("é", 300)+`"}`)
	// --values is a diff: silver goes, bronze comes, gold keeps its id.
	goldBefore := metaValueIDs(t, h, "tier")["gold"]
	edit := h.MustYes(settings.OpMetaEdit, `{"id":"tier","values":"gold, bronze"}`)
	if got := opstest.Text(edit); got != "tier: allowed values gold, bronze\n" {
		t.Errorf("edit text %q", got)
	}
	vals := metaValueIDs(t, h, "tier")
	if len(vals) != 2 || vals["gold"] != goldBefore || vals["bronze"] == "" {
		t.Errorf("values after diff %v (gold was %s)", vals, goldBefore)
	}
	h.MustYes(settings.OpMetaValueAdd, `{"key":"tier","value":"platinum"}`)
	h.MustYes(settings.OpMetaValueRename, `{"key":"tier","value":"gold","new_value":"golden"}`)
	if v := metaValueIDs(t, h, "tier"); v["golden"] != goldBefore {
		t.Errorf("value rename changed the id: %v", v)
	}
	h.MustYes(settings.OpMetaRename, `{"id":"owner","new_name":"maintainer"}`)
	keys := h.Must(settings.OpMetaKeys, `{"ids":true}`)
	if got := opstest.Text(keys); !strings.Contains(got, "\ttier\trepo\ttext\t0\tbronze, golden, platinum\n") || !strings.Contains(got, "\tmaintainer\trepo\ttext\t0\t\n") {
		t.Errorf("keys %q", got)
	}
	h.Fails(settings.OpMetaDelete, `{"id":"maintainer"}`, errs.NeedsConfirm)
	h.MustYes(settings.OpMetaDelete, `{"id":"maintainer"}`)
	h.Fails(settings.OpMetaEdit, `{"id":"maintainer","strict":true}`, errs.InTrash) // DELETION 3.2
	// after a delete the key name is free again (fresh id).
	h.MustYes(settings.OpMetaAdd, `{"name":"maintainer","type":"text"}`)
}

// metaValueIDs maps a key's allowed values to their val_ ids (read from
// the meta_values rows: the keys output lists names only).
func metaValueIDs(t *testing.T, h *opstest.H, key string) map[string]string {
	t.Helper()
	var out settings.MetaKeysOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpMetaKeys, `{"keys":[`+opstest.Quote(key)+`]}`))), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 1 {
		t.Fatalf("meta key %s: %+v", key, out.Data)
	}
	rows, err := h.App.List(context.Background(), store.SourceRepo, schema.TableMetaValues)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, r := range rows {
		if !r.Deleted && r.Fields["key_id"] != nil && r.Fields["key_id"].String() == out.Data[0].ID {
			m[r.Fields["name"].String()] = r.ID.String()
		}
	}
	return m
}

// TestSecretRuleStateMachine: add (on) → edit → off (reason) → off again
// (same row) → on (row kept: it carries a regex); off for a built-in
// rule id → on removes the row; every refusal.
func TestSecretRuleStateMachine(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	if got := opstest.Text(h.Must(settings.OpSecretRules, ``)); got != "(no rule rows: gitleaks defaults and lore's own pack apply)\n" {
		t.Errorf("empty rules %q", got)
	}
	add := h.MustYes(settings.OpSecretRuleAdd, `{"id":"acme-key","regex":"acme_[a-z0-9]{16}","keywords":"acme_","description":"acme keys"}`)
	id := h.ID(add)
	checkRefusals(t, h, []refusal{
		{settings.OpSecretRuleAdd, `{"id":"acme-key","regex":"x"}`, errs.InvalidInput, "rule acme-key already has a row in this repo"},
		{settings.OpSecretRuleAdd, `{"id":"Bad Rule","regex":"x"}`, errs.InvalidInput, "use lower-case letters, digits"},
		{settings.OpSecretRuleAdd, `{"id":"r2"}`, errs.InvalidInput, "--regex is required"},
		{settings.OpSecretRuleAdd, `{"id":"r3","regex":"("}`, errs.InvalidInput, "is not an RE2 pattern"},
		{settings.OpSecretRuleAdd, `{"id":"r4","regex":"x","entropy":"-1"}`, errs.InvalidInput, "is not a non-negative number"},
		{settings.OpSecretRuleAdd, `{"id":"r5","regex":"x","entropy":"lots"}`, errs.InvalidInput, "is not a non-negative number"},
		{settings.OpSecretRuleEdit, `{"id":"acme-key"}`, errs.InvalidInput, "nothing to change"},
		{settings.OpSecretRuleOff, `{"id":"acme-key"}`, errs.InvalidInput, "reason"},
		{settings.OpSecretRuleOff, `{"id":"acme-key","reason":"  "}`, errs.InvalidInput, "--reason is required"},
		{settings.OpSecretRuleOn, `{"id":"acme-key"}`, errs.NotFound, "acme-key is not off in this repo"},
		{settings.OpSecretRuleOn, `{"id":"never-off"}`, errs.NotFound, "is not off"},
	})
	steps := []struct{ op, in, wantText string }{
		{settings.OpSecretRuleEdit, `{"id":"acme-key","entropy":"3.5"}`, "✓ acme-key changed in this repo"},
		{settings.OpSecretRuleOff, `{"id":"acme-key","reason":"too noisy"}`, "✓ acme-key off in this repo"},
		{settings.OpSecretRuleOff, `{"id":"acme-key","reason":"still noisy"}`, "✓ acme-key off in this repo"},
		{settings.OpSecretRuleOn, `{"id":"acme-key"}`, "✓ acme-key on in this repo"},
	}
	for _, s := range steps {
		res := h.MustYes(s.op, s.in)
		if h.ID(res) != id || !strings.HasPrefix(opstest.Text(res), s.wantText) {
			t.Errorf("%s: id %s text %q", s.op, h.ID(res), opstest.Text(res))
		}
	}
	if got := opstest.Text(h.Must(settings.OpSecretRules, ``)); got != "RULE\tSTATE\tLEVEL\tWHERE\tNOTE\nacme-key\ton\trepo\tthis repo\tacme keys\n" {
		t.Errorf("rules after on %q", got)
	}
	off := h.ID(h.MustYes(settings.OpSecretRuleOff, `{"id":"generic-api-key","reason":"noise"}`))
	if got := opstest.Text(h.Must(settings.OpSecretRules, ``)); !strings.Contains(got, "generic-api-key\toff\trepo\tthis repo\tnoise\n") {
		t.Errorf("rules after off %q", got)
	}
	on := h.MustYes(settings.OpSecretRuleOn, `{"id":"generic-api-key"}`)
	if !strings.Contains(opstest.Text(on), "removed)") {
		t.Errorf("on of a built-in %q", opstest.Text(on))
	}
	if _, err := h.RunYes(settings.OpSecretRuleOn, `{"id":"generic-api-key"}`); !errs.Has(err, errs.NotFound) {
		t.Errorf("second on: %v", err)
	}
	if _, r, err := h.App.Get(context.Background(), engine.Ref{Store: store.SourceRepo, Table: schema.TableSecretRules, ID: mustID(t, off)}); err == nil && !r.Deleted {
		t.Errorf("built-in off row still live after on: %+v", r)
	}
}

// TestSecretRuleAppliesOnNextOpen: a rule row written by `secrets rule
// add` is part of the scanner the next process builds (each CLI command
// is one process): a memory holding a match then stops for a yes.
func TestSecretRuleAppliesOnNextOpen(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	sample := "acme_" + strings.Repeat("0123456789abcdef", 1)
	h.MustYes(settings.OpSecretRuleAdd, `{"id":"acme-key","regex":"acme_[a-z0-9]{16}"}`)
	next := h.ReopenCold()
	next.Fails("memory.add", `{"body":"sample `+sample+` here"}`, errs.NeedsConfirm)
	// switched off at the repo, the next process lets it through.
	next.MustYes(settings.OpSecretRuleOff, `{"id":"acme-key","reason":"samples only"}`)
	h.ReopenCold().Must("memory.add", `{"body":"sample `+sample+` here"}`)
}

// TestSecretAllow: an allow entry stores only the hash of the matched
// text (never the text), on the store that holds the row; every refusal.
func TestSecretAllow(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	mem := h.ID(h.Must("memory.add", `{"body":"the sample key is sample_0123 in the docs"}`))
	checkRefusals(t, h, []refusal{
		{settings.OpSecretAllow, `{"row_id":"nope","rule":"r","reason":"x","match":"sample_0123"}`, errs.InvalidID, ""},
		{settings.OpSecretAllow, `{"row_id":"` + mem + `","rule":"","reason":"x","match":"sample_0123"}`, errs.InvalidInput, "missing required field rule"},
		{settings.OpSecretAllow, `{"row_id":"` + mem + `","rule":"r","reason":"  ","match":"sample_0123"}`, errs.InvalidInput, "--rule and --reason are required"},
		{settings.OpSecretAllow, `{"row_id":"` + mem + `","rule":"r","reason":"x","match":"not there"}`, errs.InvalidInput, "name the field with --field"},
		{settings.OpSecretAllow, `{"row_id":"` + mem + `","rule":"r","reason":"x","field":"body","match":"not there"}`, errs.InvalidInput, "does not contain that text"},
		{settings.OpSecretAllow, `{"row_id":"` + mem + `","rule":"r","reason":"x","match":"sample_0123","match_hash":7}`, errs.InvalidInput, "not both"},
		{settings.OpSecretAllow, `{"row_id":"` + mem + `","rule":"r","reason":"x","field":"body"}`, errs.InvalidInput, "give --match"},
		{settings.OpSecretAllow, `{"row_id":"mem_01a1a679339b7c56896a5b5dd2cace7d","rule":"r","reason":"x","match":"a"}`, errs.NotFound, ""},
		{settings.OpSecretAllowRm, `{"id":"srl_01a1a679339b7c56896a5b5dd2cace7d"}`, errs.InvalidID, "not an allow id"},
	})
	res := h.MustYes(settings.OpSecretAllow, `{"row_id":"`+mem+`","rule":"generic-api-key","reason":"a doc sample","match":"sample_0123"}`)
	if got := opstest.Text(res); !strings.HasPrefix(got, "✓ generic-api-key: mem_…"+mem[len(mem)-4:]+".body marked not a secret (sal_…") {
		t.Errorf("allow text %q", got)
	}
	_, r, err := h.App.Get(context.Background(), engine.Ref{Store: store.SourceRepo, Table: schema.TableSecretAllows, ID: mustID(t, h.ID(res))})
	if err != nil {
		t.Fatal(err)
	}
	for f, v := range r.Fields {
		if strings.Contains(v.String(), "sample_0123") {
			t.Errorf("allow row field %s holds the matched text: %s", f, v.String())
		}
	}
	if got := opstest.Text(h.Must(settings.OpSecretAllowList, ``)); !strings.Contains(got, "generic-api-key") || strings.Contains(got, "sample_0123") {
		t.Errorf("allow list %q", got)
	}
	h.MustYes(settings.OpSecretAllowRm, `{"id":"`+h.ID(res)+`"}`)
	if got := opstest.Text(h.Must(settings.OpSecretAllowList, ``)); got != "(no allow entries)\n" {
		t.Errorf("after rm %q", got)
	}
}

func mustID(t *testing.T, s string) ids.AnyID {
	t.Helper()
	id, err := ids.ParseAny(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestLiveNaturalKeyRefusal: create() refuses a second live row with the
// same natural key in one place with the settings code (E_NAME_TAKEN /
// E_META_TAKEN), the same key in another place is a separate row, and a
// tombstoned key is free again.
func TestLiveNaturalKeyRefusal(t *testing.T) {
	t.Parallel()
	h := multiStore(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"server"}`},
		[2]string{settings.OpMetaAdd, `{"name":"tier","type":"text","values":"gold"}`},
		[2]string{settings.OpVarsSet, `{"name":"region","value":"eu"}`},
	)
	checkRefusals(t, h, []refusal{
		{settings.OpKindAdd, `{"kind":"server"}`, errs.NameTaken, "already exists in repo"},
		{settings.OpMetaAdd, `{"name":"tier","type":"text"}`, errs.MetaTaken, "already exists in repo"},
		{settings.OpMetaValueAdd, `{"key":"tier","value":"gold"}`, errs.MetaTaken, "already exists in repo"},
	})
	// another place: a separate row; a kind is the exception (RES 3.6: a
	// second definition of one kind name in one view is E_KIND_CONFLICT).
	checkRefusals(t, h, []refusal{{settings.OpKindAdd, `{"kind":"server","to":"kb"}`, errs.KindConflict, "may extend it"}})
	other := h.ID(h.MustYes(settings.OpKindAdd, `{"kind":"server2","to":"kb"}`))
	h.MustYes(settings.OpMetaAdd, `{"name":"tier","type":"text","to":"kb/web"}`)
	h.MustYes(settings.OpVarsSet, `{"name":"region","value":"us","to":"kb"}`)
	if v := varOf(t, h, "region"); v.Value != "eu" || v.Store != "repo" {
		t.Errorf("nearest region %+v", v)
	}
	if other == "" {
		t.Error("no id for the kb kind")
	}
}

// TestReadBackFromDisk: settings written by one process read back the same
// in the next, cold (every row decoded from its file) (row files decode json and list fields as plain values, not
// value.JSON; a reader that only accepts value.JSON would see nothing).
func TestReadBackFromDisk(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"app"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"app","field":"port","type":"number"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"app","field":"tags","type":"list"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"app","field":"env","type":"text","values":"prod,dev"}`},
		[2]string{settings.OpResAdd, `{"kind":"app","name":"api","set":["port=8080","tags=a, b","env=prod"]}`},
		[2]string{settings.OpVarsSet, `{"name":"n","value":"42","type":"number"}`},
		[2]string{settings.OpVarsSet, `{"name":"u","value":"https://a.example","type":"url","shareable":true}`},
		[2]string{settings.OpPolicySet, `{"key":"agents.person_only","value":"task.delete,rule.add"}`},
		[2]string{settings.OpPolicySet, `{"key":"meta.max_bytes","value":"99"}`},
		[2]string{settings.OpPolicySet, `{"key":"secrets.use_repo_gitleaks","value":"false"}`},
		[2]string{settings.OpMetaAdd, `{"name":"tier","type":"text","values":"gold,silver","strict":true}`},
	)
	before := map[string]string{}
	for _, o := range []string{settings.OpPolicyShow, settings.OpVarsList, settings.OpKinds, settings.OpMetaKeys} {
		before[o] = h.JSON(h.Must(o, ``))
	}
	beforeRes := h.JSON(h.Must(settings.OpResShow, `{"id":"api"}`))
	// ReopenCold: no cache, a fresh home, so every row is decoded from
	// its file.
	next := h.ReopenCold()
	for o, b := range before {
		if got := next.JSON(next.Must(o, ``)); got != b {
			t.Errorf("%s differs after reopen:\n%s\nwas\n%s", o, got, b)
		}
	}
	if got := next.JSON(next.Must(settings.OpResShow, `{"id":"api"}`)); got != beforeRes {
		t.Errorf("resource differs after reopen:\n%s\nwas\n%s", got, beforeRes)
	}
	// the closed set and the type still apply after the reopen.
	next.Fails(settings.OpResEdit, `{"id":"api","set":["env=staging"]}`, errs.ResourceInvalid)
	next.Fails(settings.OpVarsSet, `{"name":"n","value":"abc"}`, errs.InvalidInput)
	next.Fails(settings.OpPolicySet, `{"key":"meta.max_bytes","value":"100","to":"repo"}`, errs.NeedsConfirm)
	rules := showRules(t, next, "")
	if rules["agents.person_only"].Value != "task.delete, rule.add" || rules["meta.max_bytes"].Value != "99" || rules["secrets.use_repo_gitleaks"].Value != "false" {
		t.Errorf("policy after reopen %+v", rules)
	}
}
