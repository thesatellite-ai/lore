package wire

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// TestRosterRowsHoldMeta pins owner org-setup item 3 (devco D-862): a team
// and a person on the org roster hold custom fields as notes and
// resources do. team add --meta and actor edit --meta (direct roster ops:
// Run never sees their input) store the value on the roster row; team
// show and actor show print it; lore meta set/unset reach the rows by id;
// a --meta that no write took is refused, never dropped.
func TestRosterRowsHoldMeta(t *testing.T) {
	t.Parallel()
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{
			{Name: "agency", URL: orgURL, Folder: rowfs.FolderRoot},
			{Name: rosterStore, URL: orgURL, Folder: "org"},
		}, nil
	}
	// PersonOnly: the caller is the person whatever process runs the test.
	// With agent detection on, a test run under a coding tool wrote the
	// person's roster row as the agent's owner row (D-17), so the actor
	// edit below passed there and failed in a plain shell (devco D-907).
	a, err := New(Options{RepoDir: t.TempDir(), Home: t.TempDir(), FlagActor: testEmail, PersonOnly: true, OrgStores: hook, Wall: time.Now,
		Org: func(context.Context) (*identity.Org, error) {
			return &identity.Org{Name: "agency", Identity: identity.Entry{Key: testEmail}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	run := func(name, body string) (string, error) {
		t.Helper()
		e, ok := a.Registry().Lookup(name)
		if !ok {
			t.Fatalf("no op %s", name)
		}
		res, rerr := a.Run(ctx, e, op.JSONDecoder{Body: []byte(body)}, op.Caller{Yes: true})
		if rerr != nil {
			return "", rerr
		}
		out, jerr := res.JSON()
		if jerr != nil {
			t.Fatalf("%s json: %v", name, jerr)
		}
		txt, _ := res.Text()
		return string(out) + "\n" + txt, nil
	}
	if _, err = run("team.add", `{"handle":"@web","meta":["emoji=web-spider"]}`); err != nil {
		t.Fatalf("team add --meta: %v", err)
	}
	out, err := run("team.show", `{"handle":"@web"}`)
	if err != nil || !strings.Contains(out, `"emoji":"web-spider"`) || !strings.Contains(out, "emoji: web-spider") {
		t.Fatalf("team show does not hold the team's emoji (json and text): %v\n%s", err, out)
	}
	if out, err = run("team.list", `{}`); err != nil || !strings.Contains(out, `"emoji":"web-spider"`) {
		t.Fatalf("team list --json lacks the team's meta: %v\n%s", err, out)
	}
	// a person joins the roster with actor register (team add writes no
	// row for its caller); then they edit their own row
	if _, err = run("actor.register", `{}`); err != nil {
		t.Fatalf("actor register: %v", err)
	}
	if out, err = run("actor.edit", `{"actor":"`+testEmail+`","meta":["desk=4F"]}`); err != nil || !strings.Contains(out, "meta") {
		t.Fatalf("actor edit --meta only: %v\n%s", err, out)
	}
	out, err = run("actor.show", `{"id":"`+testEmail+`"}`)
	if err != nil || !strings.Contains(out, `"desk":"4F"`) || !strings.Contains(out, "desk: 4F") {
		t.Fatalf("actor show does not hold the person's desk (json and text): %v\n%s", err, out)
	}
	if out, err = run("actor.list", `{}`); err != nil || !strings.Contains(out, `"desk":"4F"`) {
		t.Fatalf("actor list --json lacks the person's meta: %v\n%s", err, out)
	}
	if out, err = run("actor.list", `{"with":["meta"]}`); err != nil || !strings.Contains(out, "meta:     desk=4F") {
		t.Fatalf("actor list --with meta does not print the person's meta line: %v\n%s", err, out)
	}
	// lore meta set / unset reach a roster row by its id
	tid := between(out0(t, run, "team.show", `{"handle":"@web"}`), `"id":"`, `"`)
	if _, err = run("meta.set", `{"id":"`+tid+`","pair":"room=web-chat"}`); err != nil {
		t.Fatalf("meta set on a team: %v", err)
	}
	if _, err = run("meta.unset", `{"id":"`+tid+`","key":"emoji"}`); err != nil {
		t.Fatalf("meta unset on a team: %v", err)
	}
	if out = out0(t, run, "team.show", `{"handle":"@web"}`); !strings.Contains(out, "room: web-chat") || strings.Contains(out, "emoji") {
		t.Fatalf("team show after meta set room / unset emoji:\n%s", out)
	}
	// a refused write stores nothing and answers its own error
	_, err = run("actor.edit", `{"actor":"@nobody","meta":["desk=1A"]}`)
	if err == nil || errs.Has(err, errs.MetaTable) {
		t.Fatalf("actor edit of an unknown person: %v, want the lookup's error", err)
	}
}

// out0 is run's output, failing the test on an error.
func out0(t *testing.T, run func(string, string) (string, error), name, body string) string {
	t.Helper()
	out, err := run(name, body)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

// between is the text of s after the first from, up to the next to.
func between(s, from, to string) string {
	_, rest, _ := strings.Cut(s, from)
	v, _, _ := strings.Cut(rest, to)
	return v
}
