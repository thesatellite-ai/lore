package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
)

// agentKey is the identity of the agent the tests run as.
const agentKey = "agent:backend-bot"

// textValues builds text fields for a fixture row.
func textValues(t *testing.T, kv map[string]string) map[string]value.Value {
	t.Helper()
	out := map[string]value.Value{}
	for k, v := range kv {
		tv, err := value.TextOf(v)
		if err != nil {
			t.Fatal(err)
		}
		out[k] = tv
	}
	return out
}

// TestAgentWritesInClientRepoStampTheOwner (D8, Q11): an agent writing
// into a client repo is recorded as its owning person: created_by,
// updated_by, the _hlc actor and the actor row there are the owner's, and
// the agent's id never reaches a committed file of the client repo. The
// same agent writing into an internal repo, or into the org store from the
// client repo, keeps its own id. An agent with no owner on any roster
// keeps its id (v0.1.17).
func TestAgentWritesInClientRepoStampTheOwner(t *testing.T) {
	t.Parallel()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	home := t.TempDir()
	internal := func(context.Context) (bool, error) { return false, nil }
	client := func(context.Context) (bool, error) { return true, nil }
	newAgentApp := func(repo string, isClient func(context.Context) (bool, error)) *App {
		a, err := New(Options{RepoDir: repo, Home: home, FlagActor: agentKey, OrgStores: func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: "northwind", URL: orgURL, Folder: "acme"}}, nil
		}, RepoIsClient: isClient})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}

	// The roster: the agent row (owner = otherActor) and the owner row.
	seedRepo := gitRepo(t)
	seed := newAgentApp(seedRepo, internal)
	withProject(t, seed)
	agent, err := seed.Actor(background())
	if err != nil {
		t.Fatal(err)
	}
	owner, err := ids.Parse[ids.Actor](otherActor)
	if err != nil {
		t.Fatal(err)
	}
	ownerRef, err := value.RefOf(owner.Any())
	if err != nil {
		t.Fatal(err)
	}
	agentFields := textValues(t, map[string]string{"kind": "agent", "display_name": "backend bot", "label": "backend"})
	agentFields[rosterFieldOwner] = ownerRef
	eng, err := seed.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eng.CreateWithID(background(), "northwind", schema.TableActors, owner.Any(), textValues(t, map[string]string{"kind": "human", "display_name": "Alice Owner"})); err != nil {
		t.Fatal(err)
	}
	if _, err = eng.CreateWithID(background(), "northwind", schema.TableActors, agent.Any(), agentFields); err != nil {
		t.Fatal(err)
	}
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}

	clientRepo := gitRepo(t)
	c := newAgentApp(clientRepo, client)
	withProject(t, c)
	out, err := run(t, c, opsmemory.OpAdd, `{"body":"client note by the agent","to":"repo"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	f := rowFile(t, memPath(clientRepo, idOf(t, out)))
	if fieldString(t, f, "created_by") != owner.String() || fieldString(t, f, "updated_by") != owner.String() || !strings.HasSuffix(fieldString(t, f, "_hlc"), owner.String()) {
		t.Fatalf("a client row by the agent must be stamped as its owner %s: %v", owner, f)
	}
	memID := idOf(t, out)
	if _, err = run(t, c, opsmemory.OpEdit, `{"id":"`+memID+`","body":"edited by the agent"}`, false); err != nil {
		t.Fatal(err)
	}
	if got := fieldString(t, rowFile(t, memPath(clientRepo, memID)), "updated_by"); got != owner.String() {
		t.Fatalf("an edit in the client repo must be stamped as the owner, got %s", got)
	}
	if _, err = run(t, c, opsmemory.OpDelete, `{"id":"`+memID+`"}`, true); err != nil {
		t.Fatal(err)
	}
	if tomb := rowFile(t, memPath(clientRepo, memID)); fieldString(t, tomb, "updated_by") != owner.String() || !strings.HasSuffix(fieldString(t, tomb, "_hlc"), owner.String()) {
		t.Fatalf("a delete in the client repo must be stamped as the owner: %v", tomb)
	}
	ownerRow := rowFile(t, filepath.Join(clientRepo, ".lore", "data", schema.TableActors, owner.String()+".json"))
	if fieldString(t, ownerRow, "kind") != "human" || fieldString(t, ownerRow, "display_name") != "Alice Owner" {
		t.Fatalf("the client's actor row is not the owner's projection: %v", ownerRow)
	}
	if err = filepath.WalkDir(filepath.Join(clientRepo, ".lore", "data"), func(p string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		if strings.Contains(string(mustRead(t, p)), agent.String()) {
			t.Errorf("the agent's id reached the client repo in %s", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stOut, err := run(t, c, opsmemory.OpAdd, `{"body":"store note by the agent","to":"northwind"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sf := rowFile(t, filepath.Join(fileuri.LocalPath(orgURL), "acme", ".lore", "data", schema.TableMemories, idOf(t, stOut)+".json"))
	if fieldString(t, sf, "created_by") != agent.String() {
		t.Fatalf("the org store keeps the agent's own id: %v", sf)
	}

	internalRepo := gitRepo(t)
	in := newAgentApp(internalRepo, internal)
	withProject(t, in)
	inOut, err := run(t, in, opsmemory.OpAdd, `{"body":"internal note","to":"repo"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldString(t, rowFile(t, memPath(internalRepo, idOf(t, inOut))), "created_by"); got != agent.String() {
		t.Fatalf("an internal repo keeps the agent's id, got %s", got)
	}
}

// TestAgentDetailsSwitch (LORE_TEAM_SPEC 7.6, D8): the org entry's
// agent_details in ~/.lore/config.yaml decides how an agent's write into a
// client repo is stamped. Absent or `internal` (the default): as its owning
// person. `client` (the opt-in): as the agent itself. Any other value fails
// the open with E_CONFIG_INVALID, never silently picking a side. An org
// entry of another name does not apply.
func TestAgentDetailsSwitch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		config  string // config.yaml body; "" writes none
		asAgent bool
		bad     bool
	}{
		{name: "no config file"},
		{name: "org entry without the key", config: "orgs:\n  - name: northwind\n"},
		{name: "internal", config: "orgs:\n  - name: northwind\n    agent_details: internal\n"},
		{name: "client", config: "orgs:\n  - name: northwind\n    agent_details: client\n", asAgent: true},
		{name: "client set on another org", config: "orgs:\n  - name: globex\n    agent_details: client\n  - name: northwind\n"},
		{name: "unknown value", config: "orgs:\n  - name: northwind\n    agent_details: Client\n", bad: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
			home := t.TempDir()
			isClient := func(context.Context) (bool, error) { return true, nil }
			newAgentApp := func(repo string, client bool) *App {
				opts := Options{RepoDir: repo, Home: home, FlagActor: agentKey, OrgStores: func(context.Context) ([]attach.Store, error) {
					return []attach.Store{{Name: "northwind", URL: orgURL, Folder: "acme"}}, nil
				}, Org: func(context.Context) (*identity.Org, error) { return &identity.Org{Name: "northwind"}, nil }}
				if client {
					opts.RepoIsClient = isClient
				}
				a, err := New(opts)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = a.Close() })
				return a
			}
			seed := newAgentApp(gitRepo(t), false)
			withProject(t, seed)
			agent, err := seed.Actor(background())
			if err != nil {
				t.Fatal(err)
			}
			owner, err := ids.Parse[ids.Actor](otherActor)
			if err != nil {
				t.Fatal(err)
			}
			ownerRef, err := value.RefOf(owner.Any())
			if err != nil {
				t.Fatal(err)
			}
			agentFields := textValues(t, map[string]string{"kind": "agent", "display_name": "backend bot"})
			agentFields[rosterFieldOwner] = ownerRef
			eng, err := seed.Engine(background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = eng.CreateWithID(background(), "northwind", schema.TableActors, owner.Any(), textValues(t, map[string]string{"kind": "human", "display_name": "Alice Owner"})); err != nil {
				t.Fatal(err)
			}
			if _, err = eng.CreateWithID(background(), "northwind", schema.TableActors, agent.Any(), agentFields); err != nil {
				t.Fatal(err)
			}
			if err = seed.Close(); err != nil {
				t.Fatal(err)
			}
			if c.config != "" {
				if err = os.WriteFile(filepath.Join(home, ConfigFile), []byte(c.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			repo := gitRepo(t)
			app := newAgentApp(repo, true)
			if c.bad {
				if _, err = app.Engine(background()); !errs.Has(err, errs.ConfigInvalid) || !strings.Contains(err.Error(), "agent_details") {
					t.Fatalf("an unknown agent_details must fail the open: %v", err)
				}
				return
			}
			withProject(t, app)
			out, err := run(t, app, opsmemory.OpAdd, `{"body":"client note","to":"repo"}`, false)
			if err != nil {
				t.Fatal(err)
			}
			want := owner.String()
			if c.asAgent {
				want = agent.String()
			}
			f := rowFile(t, memPath(repo, idOf(t, out)))
			if got := fieldString(t, f, "created_by"); got != want || !strings.HasSuffix(fieldString(t, f, "_hlc"), want) {
				t.Fatalf("created_by %s, want %s (agent %s, owner %s)", got, want, agent, owner)
			}
		})
	}
}
