package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fileForm returns the value a field holding raw decodes to when its row
// is read back from a file (value.Infer): a JSON array of objects is a
// List of JSON items, a JSON string is Text.
func fileForm(t *testing.T, raw string) value.Value {
	t.Helper()
	v, err := value.Infer([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// putTask writes a task row with extra fields straight to store n (a row
// another writer or a v0.1.17 binary left there).
func (w *f15World) putTask(t *testing.T, n store.Source, prj ids.AnyID, extra map[string]value.Value) ids.AnyID {
	t.Helper()
	id := mustID(t, ids.PrefixTask)
	fields := map[string]value.Value{
		"project_id": txt(prj.String()), "title": txt("ship"), "status": txt("todo"),
		"priority": txt("p2"), "commitment": txt("accepted"),
	}
	for k, v := range extra {
		fields[k] = v
	}
	r := row.Row{Table: schema.TableTasks, ID: id, HLC: hlc.Next(hlc.HLC{}, time.Now(), testActor), Fields: fields}
	sr, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.data[n].Open(store.OpenOptions{}).Write(context.Background(), []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestMoveRefusesSecretRefsInEveryForm: a ${secret:…} reference inside a
// json field must stop a move into a client repo whether the field holds
// the engine's in-memory JSON value or the file form (a List of JSON
// objects, which value.Infer makes of an array of objects).
func TestMoveRefusesSecretRefsInEveryForm(t *testing.T) {
	ctx := context.Background()
	const raw = `[{"task_id":"tsk_x","note":"needs ${secret:deploy_key}"}]`
	inMemory, err := value.JSONOf([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]value.Value{"in-memory json": inMemory, "file form": fileForm(t, raw)} {
		w := newF15World(t, true)
		prj := w.project(t, store.SourceRepo, "client")
		id := w.putTask(t, agency, prj, map[string]value.Value{"blocked_by": v})
		_, err := w.eng.Move(ctx, Ref{ID: id}, store.SourceRepo)
		if !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), "secret:deploy_key") {
			t.Errorf("%s: move into a client repo = %v, want the secret-ref refusal", name, err)
		}
	}
}

// TestScreenScansEveryForm: a key inside a json field is found whether the
// field holds the in-memory JSON value or its file form (a List of JSON
// items); under redact a json value is refused, never stored.
func TestScreenScansEveryForm(t *testing.T) {
	ctx := context.Background()
	raw := `[{"note":"key ` + fakeKey + `"}]`
	inMemory, err := value.JSONOf([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, onDetect := range []string{"refuse", "redact"} {
		set, err := policy.Resolve([]policy.Entry{{Level: policy.LevelRepo, Key: policy.KeyOnDetect, Value: txt(onDetect)}})
		if err != nil {
			t.Fatal(err)
		}
		f := setup(t, WithSettings(fixedSettings{set: set}))
		for name, v := range map[string]value.Value{"in-memory json": inMemory, "file form": fileForm(t, raw)} {
			_, err := f.eng.screen(ctx, call{}, store.SourceRepo, mustID(t, ids.PrefixTask), map[string]value.Value{"blocked_by": v}, []string{"blocked_by"})
			if !errs.Has(err, errs.SecretDetected) || strings.Contains(err.Error(), fakeKey) {
				t.Errorf("%s/%s: %v, want E_SECRET_DETECTED without the key", onDetect, name, err)
			}
		}
	}
}

// TestDeleteDropsTagInEveryForm: deleting a tag removes its id from a
// memory's tag_ids and keeps the other id, whether the list was written by
// the engine or holds the file form a row file may decode to (a List of
// texts, or a JSON array Infer could not type).
func TestDeleteDropsTagInEveryForm(t *testing.T) {
	ctx := context.Background()
	w := newF15World(t, false)
	prj := pinProject(t, w)
	forms := map[string]func(a, b ids.AnyID) value.Value{
		"list of texts": func(a, b ids.AnyID) value.Value { return fileForm(t, `["`+a.String()+`","`+b.String()+`"]`) },
		"json array":    func(a, b ids.AnyID) value.Value { return fileForm(t, `["`+a.String()+`",{"x":1},"`+b.String()+`"]`) },
	}
	for name, form := range forms {
		a, err := w.eng.Create(ctx, store.SourceRepo, schema.TableTags, tagFields(prj, "a-"+strings.ReplaceAll(name, " ", "_")))
		if err != nil {
			t.Fatal(err)
		}
		b, err := w.eng.Create(ctx, store.SourceRepo, schema.TableTags, tagFields(prj, "b-"+strings.ReplaceAll(name, " ", "_")))
		if err != nil {
			t.Fatal(err)
		}
		mid := mustID(t, ids.PrefixMemory)
		r := row.Row{Table: schema.TableMemories, ID: mid, HLC: hlc.Next(hlc.HLC{}, time.Now(), testActor), Fields: map[string]value.Value{
			"project_id": txt(prj.String()), "kind": txt(schema.MemoryKindCore), "body": txt("m"), "source_kind": txt("manual"),
			schema.FieldTagIDs: form(a.Row.ID, b.Row.ID),
		}}
		sr, err := store.FromRow(r)
		if err != nil {
			t.Fatal(err)
		}
		if err = w.data[store.SourceRepo].Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
			t.Fatal(err)
		}
		if _, err = hardDelete(ctx, w.eng, Ref{ID: a.Row.ID}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, got, err := w.eng.Get(ctx, Ref{ID: mid})
		if err != nil {
			t.Fatal(err)
		}
		list := strings.Join(value.ItemStrings(got.Fields[schema.FieldTagIDs]), ",")
		if strings.Contains(list, a.Row.ID.String()) {
			t.Errorf("%s: the deleted tag is still listed: %s (the dependent was not found)", name, list)
		}
		if name == "list of texts" && list != b.Row.ID.String() {
			t.Errorf("%s: tag_ids = %q, want only the other tag", name, list)
		}
	}
}
