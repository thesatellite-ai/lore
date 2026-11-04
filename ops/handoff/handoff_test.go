package handoff_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// actor writes an actors row and returns its id; empty values are left
// out.
func actor(t *testing.T, h *opstest.H, kind, display, handle, key string) string {
	t.Helper()
	fields := map[string]value.Value{}
	for k, v := range map[string]string{"kind": kind, "display_name": display, "handle": handle, "stable_key": key} {
		if v == "" {
			continue
		}
		tv, err := value.TextOf(v)
		if err != nil {
			t.Fatal(err)
		}
		fields[k] = tv
	}
	w, err := h.App.Create(context.Background(), store.SourceRepo, schema.TableActors, fields)
	if err != nil {
		t.Fatal(err)
	}
	return w.Row.ID.String()
}

// roster seeds the actors the resolution matrix runs against.
func roster(t *testing.T, h *opstest.H) map[string]string {
	t.Helper()
	return map[string]string{
		"alice": actor(t, h, "human", "Alice Liddell", "alice", "human:alice@example.com"),
		"fe":    actor(t, h, "agent", "Frontend Agent", "agent-frontend", "agent:frontend"),
		"bob1":  actor(t, h, "human", "Bob", "", "human:bob1@example.com"),
		"bob2":  actor(t, h, "human", "bob", "", "human:bob2@example.com"),
		"jose":  actor(t, h, "human", "José Ñúñez", "", "human:jose@example.com"),
	}
}

// TestForResolution is --for's matrix: act_ id, @handle, bare handle,
// stable key, the name part of a stable key, a display name in any case
// (unicode too); no match is E_NOT_FOUND, several matches is
// E_INVALID_INPUT naming them; --to is the deprecated alias.
func TestForResolution(t *testing.T) {
	h := opstest.New(t)
	ids := roster(t, h)
	cases := []struct {
		name, input, want string
		code              errs.Code
	}{
		{"act id", `"for":"` + ids["alice"] + `"`, ids["alice"], ""},
		{"@handle", `"for":"@alice"`, ids["alice"], ""},
		{"bare handle", `"for":"agent-frontend"`, ids["fe"], ""},
		{"stable key", `"for":"human:alice@example.com"`, ids["alice"], ""},
		{"key name part", `"for":"frontend"`, ids["fe"], ""},
		{"display name", `"for":"alice liddell"`, ids["alice"], ""},
		{"display name upper", `"for":"FRONTEND AGENT"`, ids["fe"], ""},
		{"unicode display", `"for":"josé ñúñez"`, ids["jose"], ""},
		{"--to alias", `"to_actor":"@alice"`, ids["alice"], ""},
		{"--for wins over --to", `"for":"@alice","to_actor":"@agent-frontend"`, ids["alice"], ""},
		{"several", `"for":"bob"`, "", errs.InvalidInput},
		{"none", `"for":"@nobody"`, "", errs.NotFound},
		{"empty handle", `"for":"@"`, "", errs.InvalidInput},
		{"missing", `"for":"  "`, "", errs.InvalidInput},
		{"absent", `"body":"x"`, "", errs.InvalidInput},
	}
	for _, c := range cases {
		in := `{"text":"picked up the retry work",` + c.input + `}`
		if c.code != "" {
			msg := h.Fails("handoff.add", in, c.code)
			if c.name == "several" && (!strings.Contains(msg, ids["bob1"]) || !strings.Contains(msg, ids["bob2"])) {
				t.Errorf("several: %q should name both actors", msg)
			}
			continue
		}
		res := h.Must("handoff.add", in)
		id := h.ID(res)
		show := h.JSON(h.Must("handoff.show", `{"id":"`+id+`"}`))
		if got := contenttest.Str(t, show, "data", "to_actor_id"); got != c.want {
			t.Errorf("%s: to_actor_id %q, want %q", c.name, got, c.want)
		}
		if got := contenttest.Str(t, show, "data", "status_str"); got != "open" {
			t.Errorf("%s: status %q, want open", c.name, got)
		}
	}
	// no refused add wrote a row: one handoff per accepted case
	var accepted int
	for _, c := range cases {
		if c.code == "" {
			accepted++
		}
	}
	if got := strings.Count(opstest.Text(h.Must("handoff.list", "")), "HA  "); got != accepted {
		t.Errorf("list has %d handoffs, want %d", got, accepted)
	}
}

// TestAddLinesAndText: the human add line echoes --for as given
// (v0.1.17 "✓ handoff <id> for <who>"); multi-word text and --body are
// both accepted, the argument wins when both are given; an empty text is
// E_EMPTY_BODY; list/show/ack/delete lines match v0.1.17.
func TestAddLinesAndText(t *testing.T) {
	h := opstest.New(t)
	ids := roster(t, h)
	res := h.Must("handoff.add", `{"for":"@alice","text":"form is half done, see PR 12"}`)
	id := h.ID(res)
	if got, want := opstest.Text(res), "✓ handoff "+id+" for @alice\n"; got != want {
		t.Errorf("add line %q, want %q", got, want)
	}
	if got := contenttest.Str(t, h.JSON(h.Must("handoff.show", `{"id":"`+id+`"}`)), "data", "body"); got != "form is half done, see PR 12" {
		t.Errorf("body %q", got)
	}
	viaBody := h.ID(h.Must("handoff.add", `{"for":"`+ids["fe"]+`","body":"via body"}`))
	if got := contenttest.Str(t, h.JSON(h.Must("handoff.show", `{"id":"`+viaBody+`"}`)), "data", "body"); got != "via body" {
		t.Errorf("--body %q", got)
	}
	h.Fails("handoff.add", `{"for":"@alice","text":"  "}`, errs.EmptyBody)
	h.Fails("handoff.add", `{"for":"@alice"}`, errs.EmptyBody)

	if got, want := opstest.Text(h.Must("handoff.show", `{"id":"`+id+`"}`)), "HA  "+id+"\n"; got != want {
		t.Errorf("show line %q", got)
	}
	doc := h.JSON(h.Must("handoff.list", ""))
	// v0.1.17 record keys (from_actor_id is only written when the
	// caller's actor row exists).
	keys := contenttest.Keys(t, doc, "data", "0")
	want := []string{"id", "created_at", "updated_at", "project_id", "to_actor_id", "body", "status_str"}
	if strings.Join(keys, " ") != strings.Join(want, " ") && strings.Join(keys, " ") != "id created_at updated_at project_id from_actor_id to_actor_id body status_str" {
		t.Errorf("record keys %v", keys)
	}
	if got, want := opstest.Text(h.Must("handoff.ack", `{"id":"`+id+`"}`)), "✓ "+id+" acked\n"; got != want {
		t.Errorf("ack line %q", got)
	}
	// ack is idempotent, as v0.1.17
	h.Must("handoff.ack", `{"id":"`+id+`"}`)
	if got := contenttest.Str(t, h.JSON(h.Must("handoff.show", `{"id":"`+id+`"}`)), "data", "status_str"); got != "acked" {
		t.Errorf("status after ack %q", got)
	}
	h.Fails("handoff.ack", `{"id":"hnd_01a1a5aca7f57659be7320c5f4b7ffff"}`, errs.NotFound)
	h.Fails("handoff.delete", `{"id":"`+id+`"}`, errs.NeedsConfirm)
	if got, want := opstest.Text(h.MustYes("handoff.delete", `{"id":"`+id+`"}`)), "✓ "+id+" deleted (hard)\n"; got != want {
		t.Errorf("delete line %q", got)
	}
	h.Fails("handoff.show", `{"id":"`+id+`"}`, errs.NotFound)
	h.Fails("handoff.ack", `{"id":"`+id+`"}`, errs.NotFound)
}

// TestTaskFillsMission: --task records the task and its mission; a task
// without a mission records no mission; an unknown or malformed task is
// refused before anything is written.
func TestTaskFillsMission(t *testing.T) {
	h := opstest.New(t)
	roster(t, h)
	mission := contenttest.Str(t, h.JSON(h.Must("mission.add", `{"title":"Ship v2"}`)), "data", "id")
	if mission == "" {
		t.Fatal("mission.add gave no id")
	}
	withM := contenttest.Str(t, h.JSON(h.Must("task.add", `{"title":"form","mission":"`+mission+`"}`)), "data", "id")
	without := contenttest.Str(t, h.JSON(h.Must("task.add", `{"title":"loose"}`)), "data", "id")
	if withM == "" || without == "" {
		t.Fatal("task.add gave no id")
	}
	id := h.ID(h.Must("handoff.add", `{"for":"@alice","task":"`+withM+`","text":"x"}`))
	show := h.JSON(h.Must("handoff.show", `{"id":"`+id+`"}`))
	if contenttest.Str(t, show, "data", "task_id") != withM || contenttest.Str(t, show, "data", "mission_id") != mission {
		t.Errorf("task/mission not recorded: %s", show)
	}
	id2 := h.ID(h.Must("handoff.add", `{"for":"@alice","task":"`+without+`","text":"y"}`))
	show = h.JSON(h.Must("handoff.show", `{"id":"`+id2+`"}`))
	if contenttest.Str(t, show, "data", "task_id") != without || strings.Contains(show, "mission_id") {
		t.Errorf("task without mission: %s", show)
	}
	before := h.JSON(h.Must("handoff.list", ""))
	h.Fails("handoff.add", `{"for":"@alice","task":"tsk_01a1a5aca7f57659be7320c5f4b7ffff","text":"z"}`, errs.NotFound)
	h.Fails("handoff.add", `{"for":"@alice","task":"`+mission+`","text":"z"}`, errs.NotFound)
	h.Fails("handoff.add", `{"for":"@alice","task":"tsk_31","text":"z"}`, errs.NotFound)
	if h.JSON(h.Must("handoff.list", "")) != before {
		t.Error("a refused --task wrote a handoff")
	}
}

// TestSearch: search covers body and status (v0.1.17 FTS of handoffs),
// with "<id> <body>" lines and the v0.1.17 hit row keys.
func TestSearch(t *testing.T) {
	h := opstest.New(t)
	roster(t, h)
	a := h.ID(h.Must("handoff.add", `{"for":"@alice","text":"retry logic is half done"}`))
	b := h.ID(h.Must("handoff.add", `{"for":"@alice","text":"nothing to see"}`))
	h.Must("handoff.ack", `{"id":"`+b+`"}`)
	doc := h.JSON(h.Must("handoff.search", `{"query":"retry"}`))
	if !strings.Contains(doc, a) || strings.Contains(doc, b) {
		t.Errorf("search retry: %s", doc)
	}
	if k := contenttest.Keys(t, doc, "data", "0", "row"); strings.Join(k, " ") != "body status_str" {
		t.Errorf("hit row keys %v", k)
	}
	st := h.JSON(h.Must("handoff.search", `{"query":"acked","columns":["status_str"]}`))
	if !strings.Contains(st, b) || strings.Contains(st, a) {
		t.Errorf("search --column status_str acked: %s", st)
	}
	if got := opstest.Text(h.Must("handoff.search", `{"query":"zzzz"}`)); got != "(no matches)\n" {
		t.Errorf("no hits %q", got)
	}
	h.Fails("handoff.search", `{"query":"x","columns":["title"]}`, errs.InvalidInput)
}
