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
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
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
	finds, err := a.DoctorSharedKeys(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(finds) != 1 || finds[0].Level != DoctorWarning || !strings.Contains(finds[0].Message, mergeFrom) || !strings.Contains(finds[0].Message, mergeInto) ||
		!strings.Contains(finds[0].Message, mergeKey) || finds[0].Fix != "lore actor merge --from "+mergeFrom+" --into "+mergeInto {
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
	if finds, err = a.DoctorSharedKeys(ctx, false); err != nil || len(finds) != 0 {
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
		got[0].Fix != "if it is the same person: lore actor merge --from "+mergeFrom+" --into "+mergeInto {
		t.Fatalf("likely duplicates = %+v", got)
	}
}

// taskAssigneeField is the task column that names its assignee.
const taskAssigneeField = "assigned_to_actor_id"

// sharedKeyRepo is a repo with two actors (ids from, into; keys fromKey,
// intoKey written into their row files as adopt writes them) and a task
// assigned to from; it returns the app reopened and the task id.
func sharedKeyRepo(t *testing.T, from, fromKey, into, intoKey string) (*App, ids.AnyID) {
	t.Helper()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	ctx := background()
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seedActor(t, eng, into, "Una Tester", intoKey, "una")
	seedActor(t, eng, from, "Una Tester", fromKey, "")
	fid, err := ids.ParseAny(from)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := value.RefOf(fid)
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
	addKeyToFile(t, repo, into, intoKey)
	addKeyToFile(t, repo, from, fromKey)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	return newTestApp(t, repo), task.Row.ID
}

// mergedState is the merged actor's row as the test checks it: kept,
// inactive, marked merged_into (or, in a repo store that keeps no
// merged_into, without the shared key).
func mergedState(t *testing.T, a *App, from string) (active string, marked bool) {
	t.Helper()
	fid, err := ids.ParseAny(from)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := a.Get(background(), engine.Ref{Store: store.SourceRepo, Table: schema.TableActors, ID: fid})
	if err != nil || r.Deleted {
		t.Fatalf("the actor %s = %+v, %v; want its row kept", from, r, err)
	}
	return fieldText(r, schema.FieldActorActive), fieldText(r, schema.FieldActorMergedInto) == mergeInto || fieldText(r, actorFieldKey) == ""
}

// TestFixMergesSharedKey (owner item 28): `lore doctor --fix --yes` runs
// the merge actor_stable_key suggests (into: the actor keepOf picks, the
// one the key derives here), prints it as fixed and re-points the rows;
// a second run finds nothing. Without --yes the warning stays, saying it
// needs --yes, and nothing is written.
func TestFixMergesSharedKey(t *testing.T) {
	t.Parallel()
	a, task := sharedKeyRepo(t, mergeFrom, mergeKey, mergeInto, mergeKey)
	fix := `{"fix":true}`

	raw, err := run(t, a, opsdoctor.OpRun, fix, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "needs --yes") || !strings.Contains(raw, "lore actor merge --from "+mergeFrom+" --into "+mergeInto) {
		t.Fatalf("fix without --yes = %s; want the warning saying it needs --yes and naming the merge", raw)
	}
	if got := assignee(t, a, task); got != mergeFrom {
		t.Fatalf("fix without --yes wrote: assignee %s", got)
	}
	if active, marked := mergedState(t, a, mergeFrom); active == "false" || marked {
		t.Fatalf("fix without --yes marked %s merged", mergeFrom)
	}

	if raw, err = run(t, a, opsdoctor.OpRun, fix, true); err != nil {
		t.Fatal(err)
	}
	var r struct {
		Fixed    []string `json:"fixed"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	want := "merged " + mergeFrom + " into " + mergeInto
	if n := len(slices.DeleteFunc(slices.Clone(r.Fixed), func(l string) bool { return !strings.Contains(l, want) })); n != 1 {
		t.Fatalf("fix --yes fixed = %q; want one line %q", r.Fixed, want)
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, string(DoctorCheckSharedKeys)) {
			t.Errorf("after fix --yes a shared key is still reported: %s", w)
		}
	}
	if got := assignee(t, a, task); got != mergeInto {
		t.Errorf("the task's assignee is %s, want %s", got, mergeInto)
	}
	if active, marked := mergedState(t, a, mergeFrom); active != "false" || !marked {
		t.Errorf("%s after fix --yes: active %q, marked %v; want inactive and marked", mergeFrom, active, marked)
	}

	if raw, err = run(t, a, opsdoctor.OpRun, fix, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "merged "+mergeFrom) || strings.Contains(raw, string(DoctorCheckSharedKeys)) {
		t.Errorf("second fix --yes = %s; want nothing merged or reported", raw)
	}
}

// TestFixLeavesDifferentKeys: two actors with different stable keys are
// two identities; fix --yes reports and merges nothing.
func TestFixLeavesDifferentKeys(t *testing.T) {
	t.Parallel()
	a, task := sharedKeyRepo(t, mergeFrom, "human:other@example.test", mergeInto, mergeKey)
	raw, err := run(t, a, opsdoctor.OpRun, `{"fix":true}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, string(DoctorCheckSharedKeys)) || strings.Contains(raw, "merged ") {
		t.Errorf("fix --yes over two keys = %s; want no actor_stable_key line", raw)
	}
	if got := assignee(t, a, task); got != mergeFrom {
		t.Errorf("the task's assignee is %s, want %s untouched", got, mergeFrom)
	}
	if active, marked := mergedState(t, a, mergeFrom); active == "false" || marked {
		t.Errorf("%s was marked merged", mergeFrom)
	}
}
