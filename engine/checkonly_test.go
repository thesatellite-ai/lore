package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// coIn / coOut are the input and output of the check-only probe below: an
// operation that, like meta set --id before H-180, forgets its --dry-run
// and calls the engine's write API either way.
type (
	coIn struct {
		Verb   string `json:"verb" desc:"create, update or delete"`
		Name   string `json:"name,omitempty" desc:"the kind's name"`
		Field  string `json:"field,omitempty" desc:"a field to set besides name"`
		ID     string `json:"id,omitempty" desc:"the kind to update or delete"`
		DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"write nothing"`
	}
	coOut struct {
		ID string `json:"id"`
	}
)

// coProbe is the probe operation over the engine of f.
func coProbe(f *fixture) op.Entry {
	return op.Define(op.Spec{Name: "probe.kind", Summary: "probe", Permission: op.PermWrite, Access: op.AccessWrite},
		func(ctx context.Context, in coIn) (coOut, error) {
			fields := map[string]value.Value{"name": txt(in.Name)}
			if in.Field != "" {
				fields[in.Field] = txt("x")
			}
			var w Written
			var err error
			switch in.Verb {
			case "create":
				w, err = f.eng.Create(ctx, store.SourceRepo, schema.TableKinds, fields)
			case "update":
				id, perr := ids.ParseAny(in.ID)
				if perr != nil {
					return coOut{}, perr
				}
				w, err = f.eng.Update(ctx, Ref{Table: schema.TableKinds, ID: id}, fields)
			case "delete":
				id, perr := ids.ParseAny(in.ID)
				if perr != nil {
					return coOut{}, perr
				}
				w, err = f.eng.Delete(ctx, Ref{Table: schema.TableKinds, ID: id})
			}
			return coOut{ID: w.Row.ID.String()}, err
		})
}

// TestDryRunIsACheckOnlyWrite (devco:D-928, H-186): a write operation's
// --dry-run runs the engine's write path check-only. Invalid input fails
// with the same code as the real write; valid input writes nothing (no
// row, no edit, no delete, no audit record), even from an operation that
// never looks at its own dry_run. Before: the dry run of such an operation
// wrote (meta set --id … --dry-run), and nothing in the engine stopped it.
func TestDryRunIsACheckOnlyWrite(t *testing.T) {
	t.Parallel()
	f := setup(t)
	probe := coProbe(f)
	run := func(body string) (coOut, error) {
		t.Helper()
		res, err := f.eng.Run(context.Background(), probe, op.JSONDecoder{Body: []byte(body)}, op.Caller{Yes: true})
		if err != nil {
			return coOut{}, err
		}
		out, _ := res.Value().Interface().(coOut)
		return out, nil
	}
	seed, err := run(`{"verb":"create","name":"contract"}`)
	if err != nil {
		t.Fatal(err)
	}

	// invalid input: the same code with and without --dry-run
	for _, body := range []string{
		`{"verb":"create","name":"invoice","field":"no_such_field"}`,
		`{"verb":"create","name":"contract"}`, // the name is taken
		`{"verb":"update","id":"` + seed.ID + `","name":"receipt","field":"no_such_field"}`,
	} {
		_, real := run(body)
		_, dry := run(body[:len(body)-1] + `,"dry_run":true}`)
		if real == nil || dry == nil {
			t.Fatalf("%s: write err %v, dry run err %v; want both to fail", body, real, dry)
		}
		if rc, dc := errs.CodeOf(real), errs.CodeOf(dry); rc != dc {
			t.Errorf("%s: write fails %s, dry run fails %s; want the same code", body, rc, dc)
		}
	}
	audited := len(f.audit.events())

	// valid input: nothing written
	made, err := run(`{"verb":"create","name":"invoice","dry_run":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if id, perr := ids.ParseAny(made.ID); perr == nil {
		if _, r, gerr := f.eng.Get(context.Background(), Ref{Table: schema.TableKinds, ID: id}); gerr == nil && !r.Deleted {
			t.Errorf("create --dry-run stored %s", made.ID)
		}
	}
	if _, err = run(`{"verb":"update","id":"` + seed.ID + `","name":"receipt","dry_run":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err = run(`{"verb":"delete","id":"` + seed.ID + `","dry_run":true}`); err != nil {
		t.Fatal(err)
	}
	id, _ := ids.ParseAny(seed.ID)
	_, r, err := f.eng.Get(context.Background(), Ref{Table: schema.TableKinds, ID: id})
	if err != nil || r.Deleted {
		t.Fatalf("delete --dry-run removed %s: %v", seed.ID, err)
	}
	if got, ok := r.Fields["name"].(value.Text); !ok || got.String() != "contract" {
		t.Errorf("update --dry-run changed the name to %v", r.Fields["name"])
	}
	if n := len(f.audit.events()); n != audited {
		t.Errorf("dry runs recorded %d audit events", n-audited)
	}
}
