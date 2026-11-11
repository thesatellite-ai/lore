package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// The two ids of one person (placeholders): the v0.1.17 repo's actor and
// the one already there, sharing one stable key.
const (
	mergeFrom = "act_541757c158dc7fb58c1c338b0a60f201"
	mergeKey  = "human:una@example.test"
)

// mergeInto is the id the key derives (as every 0.1.18 key-made actor's):
// the actor a merge keeps when neither is the roster's.
var mergeInto = func() string {
	id, err := identity.ActorID(mergeKey)
	if err != nil {
		panic(err)
	}
	return id.String()
}()

// seedActor writes an actor row with key into the repo store.
func seedActor(t *testing.T, eng *engine.Engine, id, name, key, handle string) {
	t.Helper()
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{}
	for k, v := range map[string]string{actorFieldKind: schema.ActorKindHuman, actorFieldDisplay: name, actorFieldKey: key, actorFieldHandle: handle} {
		if v == "" {
			continue
		}
		tv, terr := value.TextOf(v)
		if terr != nil {
			t.Fatal(terr)
		}
		fields[k] = tv
	}
	if _, err := eng.CreateWithID(background(), store.SourceRepo, schema.TableActors, aid, fields); err != nil {
		t.Fatal(err)
	}
}

// TestActorMergeSharedKey is the owner's two actor ids for one identity
// (2026-11-12): doctor names the pair with the merge command; the merge
// asks first, edits every row naming the duplicate to name the kept
// actor, marks the duplicate merged_into (its row stays); doctor is then
// clean and a second merge changes nothing.
func TestActorMergeSharedKey(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	ctx := background()
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seedActor(t, eng, mergeInto, "Una Tester", mergeKey, "una")
	seedActor(t, eng, mergeFrom, "Una Tester", mergeKey, "")
	from, err := ids.ParseAny(mergeFrom)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := value.RefOf(from)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{taskAssigneeField: ref}
	for k, v := range map[string]string{"title": "ship it", "status": "todo", "priority": "medium", "commitment": "accepted"} {
		tv, terr := value.TextOf(v)
		if terr != nil {
			t.Fatal(terr)
		}
		fields[k] = tv
	}
	pid, err := ids.ParseAny(projectID(t, a))
	if err != nil {
		t.Fatal(err)
	}
	if fields["project_id"], err = value.RefOf(pid); err != nil {
		t.Fatal(err)
	}
	task, err := eng.Create(ctx, store.SourceRepo, schema.TableTasks, fields)
	if err != nil {
		t.Fatal(err)
	}

	// a repo store keeps an actor's id, name and kind only (F18), so the
	// key goes into the row files the way adopt and roster copies write it
	for _, id := range []string{mergeInto, mergeFrom} {
		addKeyToFile(t, repo, id, mergeKey)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = newTestApp(t, repo)
	finds, err := a.DoctorSharedKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(finds) != 1 || finds[0].Level != DoctorWarning || !strings.Contains(finds[0].Message, mergeFrom) || !strings.Contains(finds[0].Message, mergeInto) ||
		!strings.Contains(finds[0].Message, mergeKey) || finds[0].Fix != "lore actor merge "+mergeFrom+" "+mergeInto {
		t.Fatalf("doctor = %+v, want one warning naming both ids and the merge", finds)
	}

	in := `{"from":"` + mergeFrom + `","into":"` + mergeInto + `"}`
	if _, err := run(t, a, opsinfra.OpActorMerge, in, false); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("merge without a yes = %v, want E_NEEDS_CONFIRM", err)
	}
	if got := assignee(t, a, task.Row.ID); got != mergeFrom {
		t.Fatalf("the unconfirmed merge wrote: assignee %s", got)
	}
	raw, err := run(t, a, opsinfra.OpActorMerge, in, true)
	if err != nil {
		t.Fatal(err)
	}
	var out opsinfra.ActorMergeOut
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Rows != 1 || out.Marked != 1 || out.Key != mergeKey || !slices.Contains(out.Tables, opsinfra.ActorMergeTable{Table: string(schema.TableTasks), Rows: 1}) {
		t.Fatalf("merge = %s", raw)
	}
	if got := assignee(t, a, task.Row.ID); got != mergeInto {
		t.Errorf("the task's assignee is %s, want %s", got, mergeInto)
	}
	_, fr, err := a.Get(ctx, engine.Ref{Store: store.SourceRepo, Table: schema.TableActors, ID: from})
	// a repo store keeps an actor's id, name, kind (F18): merged_into and
	// the key are Internal and stay out of it; the copy is kept, inactive
	if err != nil || fr.Deleted || fieldText(fr, schema.FieldActorActive) != "false" ||
		(fieldText(fr, schema.FieldActorMergedInto) != mergeInto && fieldText(fr, actorFieldKey) != "") {
		t.Fatalf("the merged actor = %+v, %v; want its row kept, inactive, marked merged_into %s or without the shared key", fr.Fields, err, mergeInto)
	}
	if finds, err = a.DoctorSharedKeys(ctx); err != nil || len(finds) != 0 {
		t.Fatalf("doctor after the merge = %+v, %v; want nothing", finds, err)
	}
	if raw, err = run(t, a, opsinfra.OpActorMerge, in, true); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.Rows != 0 || out.Marked != 0 {
		t.Fatalf("second merge = %s, %v; want nothing changed", raw, err)
	}
	// refusals name the cause and the next step
	if _, err := run(t, a, opsinfra.OpActorMerge, `{"from":"`+mergeInto+`","into":"`+mergeInto+`"}`, true); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("merge into itself = %v", err)
	}
	if _, err := run(t, a, opsinfra.OpActorMerge, `{"from":"act_541757c158dc7fb58c1c338b0a60f2ff","into":"`+mergeInto+`"}`, true); !errs.Has(err, errs.NotFound) ||
		!strings.Contains(err.Error(), "stores this command reads") {
		t.Errorf("merge of an unknown actor = %v", err)
	}
}

// addKeyToFile writes stable_key into the actor row file of id in repo.
func addKeyToFile(t *testing.T, repo, id, key string) {
	t.Helper()
	p := filepath.Join(repo, ".lore", "data", string(schema.TableActors), id+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc[actorFieldKey], err = json.Marshal(key); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(out, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// assignee is a task's assigned_to_actor_id.
func assignee(t *testing.T, a *App, id ids.AnyID) string {
	t.Helper()
	_, r, err := a.Get(background(), engine.Ref{Table: schema.TableTasks, ID: id})
	if err != nil {
		t.Fatal(err)
	}
	return fieldText(r, taskAssigneeField)
}

// TestLikelyHandleDuplicate: a human keyed by a git login (no email)
// whose login is a roster person's handle is a note naming both and the
// merge, never merged on its own; an email key or another handle is not.
func TestLikelyHandleDuplicate(t *testing.T) {
	t.Parallel()
	person, err := ids.ParseAny(mergeInto)
	if err != nil {
		t.Fatal(err)
	}
	login, err := ids.ParseAny(mergeFrom)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ids.ParseAny("act_541757c158dc7fb58c1c338b0a60f203")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[ids.AnyID]keyActor{
		person: {id: person, name: "Una Tester", key: mergeKey, handle: "una", roster: true},
		login:  {id: login, name: "una", key: "human:una"},
		other:  {id: other, name: "Ola", key: "human:ola"},
	}
	got := likelyHandleDuplicates(byID, map[ids.AnyID]bool{})
	if len(got) != 1 || got[0].Level != DoctorNote || !strings.Contains(got[0].Message, mergeFrom) || !strings.Contains(got[0].Message, "@una "+mergeInto) ||
		got[0].Fix != "if it is the same person: lore actor merge "+mergeFrom+" "+mergeInto {
		t.Fatalf("likely duplicates = %+v", got)
	}
}

// taskAssigneeField is the task column that names its assignee.
const taskAssigneeField = "assigned_to_actor_id"
