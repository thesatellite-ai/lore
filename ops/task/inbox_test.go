package task_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// taskStatusField is the tasks.status column (schema/builtin_work.go).
const taskStatusField = "status"

// annKey is the inbox owner's flag identity.
const annKey = "human:ann@example.com"

// otherActor is a second person writing beside the harness caller.
const otherActor = "act_0e45506e64357c94a87e70ef1c6bbbaa"

// runAs runs an operation as actor (Yes set).
func runAs(t *testing.T, h *opstest.H, actor, name, input string) op.Result {
	t.Helper()
	e, ok := h.App.Registry().Lookup(name)
	if !ok {
		t.Fatalf("no op %s", name)
	}
	a, err := ids.Parse[ids.Actor](actor)
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.App.Run(context.Background(), e, op.JSONDecoder{Body: []byte(input)}, op.Caller{Actor: a, Yes: true})
	if err != nil {
		t.Fatalf("%s as %s %s: %v", name, actor, input, err)
	}
	return res
}

// actorRow writes an actor row with a fixed id unless one exists
// (otherActor has none of its own).
func actorRow(t *testing.T, h *opstest.H, id, name string) {
	t.Helper()
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, gerr := h.App.Get(context.Background(), engine.Ref{Table: schema.TableActors, ID: aid}); gerr == nil {
		return // the flag identity wrote its own row
	}
	if _, err = h.App.CreateWithID(context.Background(), store.SourceRepo, schema.TableActors, aid, map[string]value.Value{
		"kind": text(t, schema.ActorKindHuman), "display_name": text(t, name), "stable_key": text(t, "human:"+strings.ToLower(name)),
	}); err != nil {
		t.Fatal(err)
	}
}

// inboxKinds is the harness caller's inbox: item kind -> the texts.
func inboxKinds(t *testing.T, h *opstest.H) map[string][]string {
	t.Helper()
	var out opscoord.InboxOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opscoord.OpInbox, `{}`))), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, it := range out.Data {
		got[it.Kind] = append(got[it.Kind], it.Text)
	}
	return got
}

// hasItem reports an item of kind whose text contains every part.
func hasItem(got map[string][]string, kind string, parts ...string) bool {
	for _, txt := range got[kind] {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(txt, p)
		}
		if all {
			return true
		}
	}
	return false
}

// TestInboxItemKinds is the FB-TASK-3 matrix on the cmd/lore coordinator:
// the triage owner sees every open proposed task (theirs and their own);
// one child item per sub-task of my task someone else finished, plus the
// "no open sub-tasks left" line; a parent item when my open assigned
// task's parent is closed (as a two-clone merge leaves it); a mention item
// for @me in a task body (any case, a whole word, so x@me in an address is
// not one) or in a comment; and no child or mention item when I did the
// same myself.
func TestInboxItemKinds(t *testing.T) {
	t.Parallel()
	// Coordination needs a resolved person (wire openWorld), so the App's
	// identity is a flag actor instead of the harness's fixed Options.Actor.
	h := opstest.New(t, repoClient(false), func(o *wire.Options) { o.Actor, o.FlagActor = ids.ID[ids.Actor]{}, annKey })
	ma, err := h.App.Actor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	me := ma.String()
	// Bob is a second App over the same repo: the engine stamps authorship
	// (created_by, done_by) from the App's actor, as for a teammate's process.
	ba, err := ids.Parse[ids.Actor](otherActor)
	if err != nil {
		t.Fatal(err)
	}
	bob := h.Reopen(func(o *wire.Options) { o.Actor = ba })
	actorRow(t, h, me, "Ann")
	actorRow(t, h, otherActor, "Bob")
	h.MustYes(settings.OpPolicySet, `{"key":"tasks.triage_owner","value":"`+me+`"}`)

	// triage: a task someone else proposed.
	prop := addID(h, runAs(t, bob, otherActor, opstask.OpAdd, `{"title":"proposed by them","commitment":"proposed"}`))
	own := addID(h, runAs(t, h, me, opstask.OpAdd, `{"title":"proposed by me","commitment":"proposed"}`))
	// child: my task whose sub-task they finished.
	parent := addID(h, runAs(t, h, me, opstask.OpAdd, `{"title":"my parent"}`))
	kid := addID(h, runAs(t, bob, otherActor, opstask.OpAdd, `{"title":"their child","parent":"`+parent+`"}`))
	runAs(t, bob, otherActor, opstask.OpDone, `{"id":"`+kid+`","no_lesson":true}`)
	// child I finished myself: no "sub-task is done" item for it.
	parent2 := addID(h, runAs(t, h, me, opstask.OpAdd, `{"title":"my other parent"}`))
	kid2 := addID(h, runAs(t, h, me, opstask.OpAdd, `{"title":"my child","parent":"`+parent2+`"}`))
	runAs(t, h, me, opstask.OpDone, `{"id":"`+kid2+`","no_lesson":true}`)
	// parent: my open task whose parent they closed.
	top := addID(h, runAs(t, bob, otherActor, opstask.OpAdd, `{"title":"their parent"}`))
	mine := addID(h, runAs(t, bob, otherActor, opstask.OpAdd, `{"title":"assigned to me","parent":"`+top+`","assign":["`+me+`"]}`))
	// Every op refuses this state on one clone (done/cancel: E_OPEN_CHILDREN,
	// reopening under a closed parent: E_PARENT_CLOSED); it arises when two
	// clones merge (one closed the parent, the other reopened the child). So
	// the child is finished, the parent cancelled, and the reopen is a plain
	// engine write, as the merge would leave it.
	runAs(t, bob, otherActor, opstask.OpDone, `{"id":"`+mine+`","no_lesson":true}`)
	runAs(t, bob, otherActor, opstask.OpCancel, `{"id":"`+top+`"}`)
	mid, err := ids.ParseAny(mine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.App.Update(context.Background(), engine.Ref{Store: store.SourceRepo, Table: schema.TableTasks, ID: mid},
		map[string]value.Value{taskStatusField: text(t, schema.TaskStatusTodo)}); err != nil {
		t.Fatal(err)
	}
	// mention: in a body (upper case) and in a comment; not inside an e-mail address; not my own.
	upper := strings.ToUpper("@" + me)
	ment := addID(h, runAs(t, bob, otherActor, opstask.OpAdd, `{"title":"mentions me","body":"ping `+upper+` please"}`))
	mail := addID(h, runAs(t, bob, otherActor, opstask.OpAdd, `{"title":"an address","body":"mail x@`+me+`.example"}`))
	self := addID(h, runAs(t, h, me, opstask.OpAdd, `{"title":"my own note","body":"note to @`+me+`"}`))
	cmt := addID(h, runAs(t, bob, otherActor, opstask.OpAdd, `{"title":"commented"}`))
	runAs(t, bob, otherActor, "comment.add", `{"on_table":"tasks","on_id":"`+cmt+`","body":"over to you @`+me+`"}`)

	got := inboxKinds(t, h)
	checks := []struct {
		kind  string
		parts []string
	}{
		{"triage", []string{prop, "triage it"}},
		{"triage", []string{own, "triage it"}},
		{"child", []string{parent, kid}},
		{"child", []string{parent, "no open sub-tasks left"}},
		{"parent", []string{mine, top}},
		{"mention", []string{ment, "in the description"}},
		{"mention", []string{cmt, "in a comment"}},
	}
	for _, c := range checks {
		if !hasItem(got, c.kind, c.parts...) {
			t.Errorf("no %s item with %v: %v", c.kind, c.parts, got)
		}
	}
	for _, not := range []struct{ kind, id string }{{"child", kid2}, {"mention", mail}, {"mention", self}} {
		if hasItem(got, not.kind, not.id) {
			t.Errorf("%s item for %s (own action or not a mention): %v", not.kind, not.id, got[not.kind])
		}
	}
}
