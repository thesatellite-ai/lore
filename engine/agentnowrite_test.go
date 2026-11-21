package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// legalFolder is the folder agents are kept out of in these tests.
const legalFolder store.Source = "acme/legal"

// kindWriteIn names a kind and the place a write puts it. Via, when set,
// is the --to the command runs at while its handler writes into To, so
// only the write sink (not Run's early check) can see the place.
type kindWriteIn struct {
	Name string `arg:"0" json:"name"`
	To   string `flag:"to" json:"to,omitempty"`
	Into string `flag:"into" json:"into,omitempty"`
}

// kindWriteOp writes one kinds row into in.Into (else in.To).
func kindWriteOp(f *fixture) *op.Op[kindWriteIn, memAddOut] {
	return op.Define(op.Spec{
		Name: "kind.add", Summary: "add a kind", Permission: op.PermWrite, Access: op.AccessWrite,
		Errors: []errs.Code{errs.PolicyDenied},
	}, func(ctx context.Context, in kindWriteIn) (memAddOut, error) {
		to := in.Into
		if to == "" {
			to = in.To
		}
		w, err := f.eng.Create(ctx, store.Source(to), schema.TableKinds, map[string]value.Value{"name": txt(in.Name)})
		if err != nil {
			return memAddOut{}, err
		}
		return memAddOut{ID: w.Row.ID.String()}, nil
	})
}

// rowIn names one row by id and a place.
type rowIn struct {
	ID string `arg:"0" json:"id"`
	To string `flag:"to" json:"to,omitempty"`
}

// kindMoveOp moves a kinds row from the repo to in.To (the target of Run
// stays the repo: the move's copy reaches the folder only in the sink).
func kindMoveOp(f *fixture) *op.Op[rowIn, memAddOut] {
	return op.Define(op.Spec{
		Name: "kind.move", Summary: "move a kind", Permission: op.PermWrite, Access: op.AccessWrite,
		Errors: []errs.Code{errs.PolicyDenied},
	}, func(ctx context.Context, in rowIn) (memAddOut, error) {
		id, err := ids.ParseAny(in.ID)
		if err != nil {
			return memAddOut{}, err
		}
		w, err := f.eng.Move(ctx, Ref{Store: store.SourceRepo, Table: schema.TableKinds, ID: id}, store.Source(in.To))
		if err != nil {
			return memAddOut{}, err
		}
		return memAddOut{ID: w.Row.ID.String()}, nil
	})
}

// policyDeleteOp deletes a policies row of in.To (`lore policy unset`).
func policyDeleteOp(f *fixture) *op.Op[rowIn, memAddOut] {
	return op.Define(op.Spec{
		Name: "policy.unset", Summary: "unset", Permission: op.PermWrite, Access: op.AccessWrite,
		Errors: []errs.Code{errs.PolicyDenied},
	}, func(ctx context.Context, in rowIn) (memAddOut, error) {
		id, err := ids.ParseAny(in.ID)
		if err != nil {
			return memAddOut{}, err
		}
		w, err := f.eng.Delete(ctx, Ref{Store: store.Source(in.To), Table: schema.TablePolicies, ID: id})
		if err != nil {
			return memAddOut{}, err
		}
		return memAddOut{ID: w.Row.ID.String()}, nil
	})
}

// legalSetup is the engine fixture plus the folder acme/legal whose own
// policies list items in agents.no_write (none: no row).
func legalSetup(t *testing.T, items ...string) (*fixture, ids.AnyID) {
	t.Helper()
	f := setup(t, WithStore(Attached{Name: legalFolder, Backend: memstore.NewData().Open(store.OpenOptions{}), Level: policy.LevelFolder}))
	if len(items) == 0 {
		return f, ids.AnyID{}
	}
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	val, err := value.JSONOf(raw)
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.eng.Create(context.Background(), legalFolder, schema.TablePolicies, map[string]value.Value{
		"key": txt(string(policy.KeyAgentsNoWrite)), "value": val, "scope": txt("folder"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, w.Row.ID
}

// liveKinds counts the live kinds rows of an attached store.
func liveKinds(t *testing.T, f *fixture, src store.Source) int {
	t.Helper()
	rows, err := f.eng.List(context.Background(), src, schema.TableKinds)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

// argsTo is the decoder of one positional argument and --flag value pairs.
func argsTo(arg string, flags ...string) op.ArgsDecoder {
	d := op.ArgsDecoder{Args: []string{arg}, Flags: map[string][]string{}}
	for i := 0; i+1 < len(flags); i += 2 {
		d.Flags[flags[i]] = append(d.Flags[flags[i]], flags[i+1])
	}
	return d
}

const (
	codexAgent = "agent:auto/codex/1a2b3c"
	otherAgent = "agent:auto/othertool/1a2b3c"
	botAgent   = "agent:release-bot"
)

// TestAgentNoWriteMatrix (owner org-setup item 6, devco D-869):
// agents.no_write in a folder's own policies refuses every write an agent
// it lists makes into that folder, whatever command makes it (a direct
// add, a write reaching the folder from a command run at another place, a
// move into it, the agent taking the policy off), with E_POLICY_DENIED
// naming the key, the place and the command a person runs; a person, an
// agent the list does not name, and writes into other places pass.
func TestAgentNoWriteMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		items  []string
		agent  string
		refuse bool
	}{
		{"star refuses every agent", []string{"*"}, codexAgent, true},
		{"star refuses an agent:label", []string{"*"}, botAgent, true},
		{"tool name refuses that tool", []string{"codex"}, codexAgent, true},
		{"tool name is case-insensitive", []string{"Codex"}, codexAgent, true},
		{"label refuses agent:label", []string{"release-bot"}, botAgent, true},
		{"full key refuses that agent", []string{codexAgent}, codexAgent, true},
		{"another tool passes", []string{"codex"}, otherAgent, false},
		{"a person always passes", []string{"*"}, "", false},
		{"no policy row: nobody is refused", nil, codexAgent, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f, _ := legalSetup(t, c.items...)
			ctx := context.Background()
			call := op.Caller{Actor: testActor, Agent: c.agent, Yes: true}
			paths := []struct {
				name string
				args op.ArgsDecoder
			}{
				{"direct --to the folder", argsTo("k1", "to", string(legalFolder))},
				{"written into the folder by a command run at the repo", argsTo("k2", "into", string(legalFolder))},
			}
			for _, p := range paths {
				before := liveKinds(t, f, legalFolder)
				_, err := f.eng.Run(ctx, kindWriteOp(f), p.args, call)
				if !c.refuse {
					if err != nil {
						t.Fatalf("%s: must pass: %v", p.name, err)
					}
					continue
				}
				if !errs.Has(err, errs.PolicyDenied) {
					t.Fatalf("%s: want E_POLICY_DENIED, got %v", p.name, err)
				}
				var e *errs.Error
				if !errors.As(err, &e) || !e.NeedsPerson {
					t.Fatalf("%s: the refusal is needs_person: %v", p.name, err)
				}
				for _, want := range []string{string(policy.KeyAgentsNoWrite), string(legalFolder), "lore policy unset --key agents.no_write --to acme/legal --yes"} {
					if !containsText(err, want) {
						t.Fatalf("%s: refusal %q lacks %q", p.name, err, want)
					}
				}
				if got := liveKinds(t, f, legalFolder); got != before {
					t.Fatalf("%s: nothing may be written, the folder went from %d to %d kinds", p.name, before, got)
				}
			}
			// the same agent writes into the repo next to the folder freely
			if _, err := f.eng.Run(ctx, kindWriteOp(f), op.ArgsDecoder{Args: []string{"k3"}}, call); err != nil {
				t.Fatalf("the repo is another place: %v", err)
			}
		})
	}
}

// TestAgentNoWriteMoveAndUnset: a move of a row into a folder that keeps
// agents out is refused for the agent (the copy is a write there) and the
// row stays where it was; the agent cannot take the policy off the folder
// either (its delete of the policy row is a write into the same place),
// while a person can.
func TestAgentNoWriteMoveAndUnset(t *testing.T) {
	t.Parallel()
	f, pol := legalSetup(t, "*")
	ctx := context.Background()
	agent := op.Caller{Actor: testActor, Agent: codexAgent, Yes: true}
	person := op.Caller{Actor: testActor, Yes: true}
	w, err := f.eng.Create(ctx, store.SourceRepo, schema.TableKinds, map[string]value.Value{"name": txt("contract")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.eng.Run(ctx, kindMoveOp(f), argsTo(w.Row.ID.String(), "to", string(legalFolder)), agent)
	if !errs.Has(err, errs.PolicyDenied) {
		t.Fatalf("an agent's move into the folder must be refused: %v", err)
	}
	if n := liveKinds(t, f, legalFolder); n != 0 {
		t.Fatalf("the refused move left %d kinds in the folder", n)
	}
	if n := liveKinds(t, f, store.SourceRepo); n != 1 {
		t.Fatalf("the refused move must leave the row in the repo, it holds %d", n)
	}
	unset := argsTo(pol.String(), "to", string(legalFolder))
	if _, err = f.eng.Run(ctx, policyDeleteOp(f), unset, agent); !errs.Has(err, errs.PolicyDenied) {
		t.Fatalf("an agent may not take the policy off the folder: %v", err)
	}
	if _, err = f.eng.Run(ctx, policyDeleteOp(f), unset, person); err != nil {
		t.Fatalf("a person takes it off: %v", err)
	}
	if _, err = f.eng.Run(ctx, kindMoveOp(f), argsTo(w.Row.ID.String(), "to", string(legalFolder)), agent); err != nil {
		t.Fatalf("with the policy gone the agent's move passes: %v", err)
	}
}

// TestAgentNames: the names an agents.no_write item may use for an agent.
func TestAgentNames(t *testing.T) {
	t.Parallel()
	for key, want := range map[string][]string{
		codexAgent:  {codexAgent, "auto/codex/1a2b3c", "codex"},
		botAgent:    {botAgent, "release-bot"},
		"something": {"something"},
	} {
		got := agentNames(key)
		if len(got) != len(want) {
			t.Fatalf("agentNames(%q) = %v, want %v", key, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("agentNames(%q) = %v, want %v", key, got, want)
			}
		}
	}
}

// containsText reports whether err's text (message and hint) holds s.
func containsText(err error, s string) bool {
	var e *errs.Error
	if errors.As(err, &e) {
		return strings.Contains(e.Message+" "+e.Hint, s)
	}
	return strings.Contains(err.Error(), s)
}

// TestAgentNoWriteAgentSource: a caller whose agent no surface filled in
// (a CLI path that runs the operation without the detected agent) is
// still judged through the engine's AgentSource; a surface that decided
// the caller is a person (AgentFixed, REST with no X-Lore-Agent) is not.
func TestAgentNoWriteAgentSource(t *testing.T) {
	t.Parallel()
	f, _ := legalSetup(t, "codex")
	f.eng.SetAgentSource(func(context.Context) string { return codexAgent })
	ctx := context.Background()
	in := argsTo("k1", "to", string(legalFolder))
	if _, err := f.eng.Run(ctx, kindWriteOp(f), in, op.Caller{Actor: testActor, Yes: true}); !errs.Has(err, errs.PolicyDenied) {
		t.Fatalf("the AgentSource's agent must be refused: %v", err)
	}
	if _, err := f.eng.Run(ctx, kindWriteOp(f), in, op.Caller{Actor: testActor, AgentFixed: true, Yes: true}); err != nil {
		t.Fatalf("a surface that fixed the caller as a person passes: %v", err)
	}
}
