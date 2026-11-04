package infra_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// fixedAt is the harness clock: the project's "created:" line on
// `project show` is printed from it.
var fixedAt = time.Date(2026, 11, 4, 12, 13, 25, 0, time.UTC)

// ticking is a clock starting at at and moving one millisecond per read:
// rows created one after the other get distinct, ordered created_at
// stamps (a frozen clock would tie them and leave the order to the ids),
// while second-precision lines still print at.
func ticking(at time.Time) func(*wire.Options) {
	var mu sync.Mutex
	n := 0
	return func(o *wire.Options) {
		o.Wall = func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			n++
			return at.Add(time.Duration(n) * time.Millisecond)
		}
	}
}

// TestRepoAddMountNameMatrix checks the mount-name rule against
// v0.1.17's textnorm.ValidateIdentifier (outputs taken from the v0.1.17
// binary: `repo add Web` / `bad name` / `wéb` are E_INVALID_IDENTIFIER,
// " web2 " registers web2, a taken name is E_MOUNT_NAME_TAKEN).
func TestRepoAddMountNameMatrix(t *testing.T) {
	h := opstest.New(t)
	h.Must(infra.OpRepoAdd, `{"mount_name":"web"}`)
	cases := []struct {
		in   string
		want string    // registered mount name ("" when refused)
		code errs.Code // refusal code
		msg  string    // part of the refusal message
	}{
		{in: "web2", want: "web2"},
		{in: " spaced ", want: "spaced"},
		{in: "a.b_c-1", want: "a.b_c-1"},
		{in: "-", want: "-"},
		{in: "0", want: "0"},
		{in: strings.Repeat("x", 300), want: strings.Repeat("x", 300)},
		{in: "Web", code: errs.InvalidIdentifier, msg: `mount_name "Web" failed validation`},
		{in: "WEB3", code: errs.InvalidIdentifier, msg: `mount_name "WEB3" failed validation`},
		{in: "bad name", code: errs.InvalidIdentifier, msg: `mount_name "bad name" failed validation`},
		{in: "wéb", code: errs.InvalidIdentifier, msg: `mount_name "wéb" failed validation`},
		{in: "日本", code: errs.InvalidIdentifier},
		{in: "web/x", code: errs.InvalidIdentifier},
		{in: "   ", code: errs.InvalidIdentifier},
		{in: "web", code: errs.MountNameTaken, msg: `mount_name "web" already exists in this project`},
		{in: " web ", code: errs.MountNameTaken},
		// An empty name is refused by the op layer's required-field check
		// before the identifier rule runs (v0.1.17: E_INVALID_IDENTIFIER).
		{in: "", code: errs.InvalidInput},
	}
	for _, c := range cases {
		res, err := h.Run(infra.OpRepoAdd, `{"mount_name":`+opstest.Quote(c.in)+`}`)
		if c.want == "" {
			if !errs.Has(err, c.code) || !strings.Contains(errText(err), c.msg) {
				t.Errorf("%q: err %v, want %s %q", c.in, err, c.code, c.msg)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		wantLine := "✓ repo " + c.want + " registered (" + h.ID(res) + ")\n"
		if got := opstest.Text(res); got != wantLine {
			t.Errorf("%q: text %q, want %q", c.in, got, wantLine)
		}
	}
	list := h.Must(infra.OpRepoList, ``)
	var got infra.RepoListOut
	if err := json.Unmarshal([]byte(h.JSON(list)), &got); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, r := range got.Data {
		names[r.MountName] = true
	}
	for _, n := range []string{"web", "web2", "spaced", "a.b_c-1", "-", "0"} {
		if !names[n] {
			t.Errorf("repo %q not listed: %v", n, names)
		}
	}
	for _, n := range []string{"Web", "web3", "WEB3"} {
		if names[n] {
			t.Errorf("refused name %q was registered", n)
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestRepoLifecycleText walks add / list / archive / unarchive / delete
// and compares each human line with v0.1.17's (ids normalised).
func TestRepoLifecycleText(t *testing.T) {
	h := opstest.New(t)
	if got := opstest.Text(h.Must(infra.OpRepoList, ``)); got != "(no repos)\n" {
		t.Errorf("empty list %q", got)
	}
	web := h.ID(h.Must(infra.OpRepoAdd, `{"mount_name":"web","origin":"git@github.com:a/web.git"}`))
	api := h.ID(h.Must(infra.OpRepoAdd, `{"mount_name":"api","display_name":"API svc"}`))
	steps := []struct {
		op, in string
		yes    bool
		want   string
	}{
		{op: infra.OpRepoList, want: "rep_ID  web  git@github.com:a/web.git\nrep_ID  api\n"},
		{op: infra.OpRepoArchive, in: api, want: "✓ api archived\n"},
		{op: infra.OpRepoList, want: "rep_ID  web  git@github.com:a/web.git\nrep_ID  api [archived]\n"},
		{op: infra.OpRepoArchive, in: web, want: "✓ web archived\n"},
		{op: infra.OpRepoList, want: "rep_ID  web  git@github.com:a/web.git [archived]\nrep_ID  api [archived]\n"},
		{op: infra.OpRepoUnarchive, in: api, want: "✓ api unarchived\n"},
		{op: infra.OpRepoUnarchive, in: api, want: "✓ api unarchived\n"},
		{op: infra.OpRepoDelete, in: api, yes: true, want: "✓ api deleted\n"},
		{op: infra.OpRepoList, want: "rep_ID  web  git@github.com:a/web.git [archived]\n"},
	}
	for i, s := range steps {
		in := ``
		if s.in != "" {
			in = `{"id":"` + s.in + `"}`
		}
		var res = h.Must
		if s.yes {
			res = h.MustYes
		}
		if got := opstest.IDPattern(opstest.Text(res(s.op, in))); got != s.want {
			t.Errorf("step %d %s: got %q, want %q", i, s.op, got, s.want)
		}
	}
	// list --json: v0.1.17's ent field order, archived_at stamped.
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(h.JSON(h.Must(infra.OpRepoList, ``))), &out); err != nil {
		t.Fatal(err)
	}
	if string(out["kind"]) != `"repo.list"` || string(out["schema_version"]) != "1" || string(out["count"]) != "1" {
		t.Errorf("envelope %v", out)
	}
	raw := h.JSON(h.Must(infra.OpRepoList, ``))
	order := []string{`"id"`, `"created_at"`, `"updated_at"`, `"project_id"`, `"mount_name"`, `"origin_url"`, `"archived_at"`}
	last := -1
	for _, k := range order {
		i := strings.Index(raw, k)
		if i <= last {
			t.Errorf("key %s out of v0.1.17 order in %s", k, raw)
		}
		last = i
	}
}

// TestRepoDeleteIsATombstone: delete asks first, keeps a tombstone (a
// second delete is E_NOT_FOUND) and the name can be registered again
// under a fresh id.
func TestRepoDeleteIsATombstone(t *testing.T) {
	h := opstest.New(t)
	id := h.ID(h.Must(infra.OpRepoAdd, `{"mount_name":"api"}`))
	h.Fails(infra.OpRepoDelete, `{"id":"`+id+`"}`, errs.NeedsConfirm)
	h.MustYes(infra.OpRepoDelete, `{"id":"`+id+`"}`)
	if _, err := h.RunYes(infra.OpRepoDelete, `{"id":"`+id+`"}`); !errs.Has(err, errs.NotFound) {
		t.Errorf("second delete: %v", err)
	}
	h.Fails(infra.OpRepoArchive, `{"id":"`+id+`"}`, errs.NotFound)
	again := h.ID(h.Must(infra.OpRepoAdd, `{"mount_name":"api"}`))
	if again == id {
		t.Errorf("re-added repo reused the tombstoned id %s", id)
	}
}

// TestIDRefusals: a malformed or wrong-prefix id is E_INVALID_ID (the
// core's id check; v0.1.17 said E_NOT_FOUND for both), a well-formed id
// of no row is E_NOT_FOUND with v0.1.17's text.
func TestIDRefusals(t *testing.T) {
	h := opstest.New(t)
	for _, o := range []string{infra.OpRepoArchive, infra.OpRepoUnarchive, infra.OpProjectArchive, infra.OpProjectUnarchive} {
		for _, bad := range []string{"rep_0000", "", "mem_01a1a679339b7c56896a5b5dd2cace7d", "prj_zz", "x"} {
			if _, err := h.RunYes(o, `{"id":`+opstest.Quote(bad)+`}`); !errs.Has(err, errs.InvalidID) && !errs.Has(err, errs.InvalidInput) {
				t.Errorf("%s %q: %v", o, bad, err)
			}
		}
	}
	msg := h.Fails(infra.OpRepoArchive, `{"id":"rep_63827c858f9d78b8816a0cf4e3f7817a"}`, errs.NotFound)
	if !strings.Contains(msg, `repo "rep_63827c858f9d78b8816a0cf4e3f7817a" not found`) {
		t.Errorf("not-found text %q", msg)
	}
	for _, bad := range []string{"act_0000", "mem_01a1a679339b7c56896a5b5dd2cace7d", opstest.Actor} {
		msg := h.Fails(infra.OpActorShow, `{"id":`+opstest.Quote(bad)+`}`, errs.NotFound)
		if !strings.Contains(msg, `actor "`+bad+`" not found`) {
			t.Errorf("actor show %q: %q", bad, msg)
		}
	}
}

// TestProjectText compares project list / show / archive with v0.1.17
// (the machine-local "active:" line and last_active_at are gone, spec).
func TestProjectText(t *testing.T) {
	h := opstest.New(t, ticking(fixedAt))
	h.Must(infra.OpRepoAdd, `{"mount_name":"web","origin":"git@github.com:a/web.git"}`)
	h.Must(infra.OpRepoAdd, `{"mount_name":"api"}`)
	if got := opstest.IDPattern(opstest.Text(h.Must(infra.OpProjectList, ``))); got != "prj_ID  demo\n" {
		t.Errorf("list %q", got)
	}
	want := "Project: prj_ID\n  name:    demo\n  created: 2026-11-04 12:13:25Z\n\nRepos:\n  rep_ID  web\n  rep_ID  api\n"
	if got := opstest.IDPattern(opstest.Text(h.Must(infra.OpProjectShow, `{"id":"`+h.Project+`"}`))); got != want {
		t.Errorf("show:\n%q\nwant\n%q", got, want)
	}
	if got := opstest.Text(h.Must(infra.OpProjectArchive, `{"id":"`+h.Project+`"}`)); got != "✓ demo archived\n" {
		t.Errorf("archive %q", got)
	}
	if got := opstest.IDPattern(opstest.Text(h.Must(infra.OpProjectList, ``))); got != "prj_ID  demo [archived]\n" {
		t.Errorf("archived list %q", got)
	}
	if got := opstest.Text(h.Must(infra.OpProjectUnarchive, `{"id":"`+h.Project+`"}`)); got != "✓ demo unarchived\n" {
		t.Errorf("unarchive %q", got)
	}
	raw := h.JSON(h.Must(infra.OpProjectShow, `{"id":"`+h.Project+`"}`))
	for _, k := range []string{`"kind":"project.show"`, `"name":"demo"`, `"created_at":"2026-11-04T12:13:25Z"`, `"mount_name":"web","origin_url":"git@github.com:a/web.git"`} {
		if !strings.Contains(raw, k) {
			t.Errorf("show json lacks %s: %s", k, raw)
		}
	}
	h.Fails(infra.OpProjectDelete, `{"id":"`+h.Project+`"}`, errs.NeedsConfirm)
}

// TestActorText: actor list / show lines as v0.1.17 printed them, from a
// roster row (kinds × optional fields).
func TestActorText(t *testing.T) {
	h := opstest.New(t)
	if got := opstest.Text(h.Must(infra.OpActorList, ``)); got != "(no actors)\n" {
		t.Errorf("empty %q", got)
	}
	ctx := context.Background()
	mk := func(fields map[string]string) string {
		f := map[string]value.Value{}
		for k, v := range fields {
			tv, err := value.TextOf(v)
			if err != nil {
				t.Fatal(err)
			}
			f[k] = tv
		}
		w, err := h.App.Create(ctx, store.SourceRepo, schema.TableActors, f)
		if err != nil {
			t.Fatal(err)
		}
		return w.Row.ID.String()
	}
	human := mk(map[string]string{"kind": "human", "display_name": "khanakia", "stable_key": "human:khanakia@ex130r"})
	mk(map[string]string{"kind": "agent", "display_name": "Ünïcode bot", "stable_key": "agent:bot"})
	if got := opstest.IDPattern(opstest.Text(h.Must(infra.OpActorList, ``))); got != "act_ID [human] khanakia\nact_ID [agent] Ünïcode bot\n" {
		t.Errorf("list %q", got)
	}
	want := human + "\n  kind:        human\n  display:     khanakia\n  stable_key:  human:khanakia@ex130r\n"
	show := h.Must(infra.OpActorShow, `{"id":" `+human+` "}`)
	if got := opstest.Text(show); got != want {
		t.Errorf("show %q want %q", got, want)
	}
	if raw := h.JSON(show); !strings.Contains(raw, `"active":true`) || !strings.Contains(raw, `"kind":"actor.show"`) {
		t.Errorf("show json %s", raw)
	}
}
