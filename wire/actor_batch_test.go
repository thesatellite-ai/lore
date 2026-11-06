package wire

// actor_batch_test.go — G-ENGINE-2 (0f4d62b9): a create writes the caller's
// actor row and the row as ONE batch, so a refused create writes nothing.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// tableFiles lists every row file under repo/.lore/data/<table>/.
func tableFiles(t *testing.T, repo string, table string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repo, ".lore", "data", table))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TestRefusedCreateWritesNoActorRow: in an own repo and in a client repo,
// a create refused by the secret check (no answer under ask) or by an
// unknown assignee writes no row file at
// all, not even the caller's actor row; the caller's next good create
// then writes the actor row with the row (the refusal left no "already
// written" mark behind).
func TestRefusedCreateWritesNoActorRow(t *testing.T) {
	who, err := identity.ActorID(testEmail)
	if err != nil {
		t.Fatal(err)
	}
	fakeKey := "AKIA" + strings.Repeat("Q", 16)
	refusals := []struct {
		name string
		op   string
		in   string
		code errs.Code
	}{
		{"secret without an answer", opsmemory.OpAdd, `{"body":"key ` + fakeKey + ` end"}`, errs.NeedsConfirm},
		{"unknown assignee", opstask.OpAdd, `{"title":"x","assign":["@nobody_here"]}`, errs.NotFound},
	}
	for _, class := range []struct {
		name   string
		client bool
	}{{"own repo", false}, {"client repo", true}} {
		for _, r := range refusals {
			t.Run(class.name+"/"+r.name, func(t *testing.T) {
				repo := gitRepo(t)
				client := class.client
				a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return client, nil }})
				withProject(t, a)
				if got := tableFiles(t, repo, schema.TableActors); len(got) != 0 {
					t.Fatalf("actor rows before any write: %v", got)
				}
				_, rerr := run(t, a, r.op, r.in, false)
				if rerr == nil || (r.code != errs.NotFound && !errs.Has(rerr, r.code)) {
					t.Fatalf("want a refusal (%s), got %v", r.code, rerr)
				}
				for _, table := range []string{schema.TableActors, schema.TableMemories, schema.TableTasks} {
					if got := tableFiles(t, repo, table); len(got) != 0 {
						t.Fatalf("a refused create left %s: %v (%v)", table, got, rerr)
					}
				}
				if _, err := run(t, a, opsmemory.OpAdd, `{"body":"a good note"}`, true); err != nil {
					t.Fatalf("good write after the refusal: %v", err)
				}
				if got := tableFiles(t, repo, schema.TableActors); len(got) != 1 || got[0] != who.String()+".json" {
					t.Fatalf("the good write did not write the caller's actor row: %v", got)
				}
			})
		}
	}
}

// TestClientProjectionLandsWithTheRow: in a client repo, a task assigned
// to a teammate whose actor row lives only in the attached internal store
// kb writes the teammate's client projection (no stable key, no handle)
// together with the task; the same create refused by the secret check
// writes neither the projection nor the task.
func TestClientProjectionLandsWithTheRow(t *testing.T) {
	ctx := context.Background()
	repo := gitRepo(t)
	kb := folderStoreAt(t, "sto_0e45506e64357c94a87e70ef1c6b0001", "internal")
	writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+kb+"\n")
	a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return true, nil }})
	withProject(t, a)
	mate, err := identity.ActorID("human:bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{}
	for k, v := range map[string]string{actorFieldKind: schema.ActorKindHuman, actorFieldDisplay: "Bob", actorFieldKey: "human:bob@example.com", actorFieldHandle: "bob"} {
		tv, terr := value.TextOf(v)
		if terr != nil {
			t.Fatal(terr)
		}
		fields[k] = tv
	}
	if _, err = a.CreateWithID(ctx, "kb", schema.TableActors, mate.Any(), fields); err != nil {
		t.Fatalf("teammate in kb: %v", err)
	}
	mateFile := filepath.Join(repo, ".lore", "data", schema.TableActors, mate.String()+".json")
	fakeKey := "AKIA" + strings.Repeat("Q", 16)
	if _, err = run(t, a, opstask.OpAdd, `{"title":"key `+fakeKey+`","assign":["`+mate.String()+`"]}`, false); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("secret in the title: %v, want E_NEEDS_CONFIRM", err)
	}
	if _, serr := os.Stat(mateFile); !os.IsNotExist(serr) {
		t.Fatalf("a refused create left the teammate's projection: %v", serr)
	}
	if got := tableFiles(t, repo, schema.TableTasks); len(got) != 0 {
		t.Fatalf("a refused create left tasks: %v", got)
	}
	if _, err = run(t, a, opstask.OpAdd, `{"title":"review","assign":["`+mate.String()+`"]}`, true); err != nil {
		t.Fatalf("task for the teammate: %v", err)
	}
	body := string(mustRead(t, mateFile))
	if strings.Contains(body, "bob@example.com") || strings.Contains(body, `"handle"`) || !strings.Contains(body, "Bob") {
		t.Fatalf("client projection of the teammate:\n%s", body)
	}
	if got := tableFiles(t, repo, schema.TableTasks); len(got) != 1 {
		t.Fatalf("task rows = %v", got)
	}
}
