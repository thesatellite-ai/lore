package task_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestAgentAddNeedsCommitment (BUGFIX-20): an agent's task add with no
// --commitment is E_INVALID_INPUT naming --commitment, as v0.1.17 refused
// it and the directive says; with one it lands; a person and an agent's
// sub-task (inherits its parent's) keep the default.
func TestAgentAddNeedsCommitment(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	e, ok := h.App.Registry().Lookup(opstask.OpAdd)
	if !ok {
		t.Fatal("no task.add")
	}
	agent := op.Caller{Agent: "agent:bot"}
	runAs := func(c op.Caller, in string) (op.Result, error) {
		return h.App.Run(context.Background(), e, op.JSONDecoder{Body: []byte(in)}, c)
	}
	_, err := runAs(agent, `{"title":"x"}`)
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "--commitment") {
		t.Fatalf("agent without --commitment: err = %v, want E_INVALID_INPUT naming --commitment", err)
	}
	res, err := runAs(agent, `{"title":"x","commitment":"proposed"}`)
	if err != nil {
		t.Fatalf("agent with --commitment: %v", err)
	}
	parent := addID(h, res)
	if _, err := runAs(agent, `{"title":"child","parent":"`+parent+`"}`); err != nil {
		t.Fatalf("agent sub-task (inherits): %v", err)
	}
	if _, err := runAs(op.Caller{}, `{"title":"by a person"}`); err != nil {
		t.Fatalf("person without --commitment: %v", err)
	}
}
