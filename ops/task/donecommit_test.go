package task

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/internal/work"
)

// refEnv is a work.Env that resolves commit refs from a fixed table (the
// embedded nil Env is never called by resolveDoneCommit).
type refEnv struct {
	work.Env
	refs map[string]string
}

func (e refEnv) ResolveCommitRef(_ context.Context, ref string) (string, error) {
	if sha, ok := e.refs[ref]; ok {
		return sha, nil
	}
	return "", errors.New("unknown revision")
}

// TestDoneCommitIsResolvedToASha (devco:M-5): task done --commit HEAD (or
// a branch, or a short sha) records the full sha of the commit it names
// when the done runs; a ref that does not resolve is refused, and an env
// without git never keeps a symbolic name. Before the fix "HEAD" was
// stored as is and the merge watch resolved it in another clone.
func TestDoneCommitIsResolvedToASha(t *testing.T) {
	ctx := context.Background()
	const full = "4d3dc0774d3dc0774d3dc0774d3dc0774d3dc077"
	o := ops{env: refEnv{refs: map[string]string{"HEAD": full, "feat/pin": full, "4d3dc07": full}}}
	for _, ref := range []string{"HEAD", " feat/pin ", "4d3dc07"} {
		got, err := o.resolveDoneCommit(ctx, "tsk_1", ref)
		if err != nil || got != full {
			t.Errorf("--commit %q: %q %v, want %s", ref, got, err, full)
		}
	}
	if got, err := o.resolveDoneCommit(ctx, "tsk_1", ""); err != nil || got != "" {
		t.Errorf("no --commit: %q %v, want empty", got, err)
	}
	if _, err := o.resolveDoneCommit(ctx, "tsk_1", "no-such-branch"); errs.CodeOf(err) != errs.InvalidInput {
		t.Errorf("unresolvable ref: %v, want E_INVALID_INPUT", err)
	}
	plain := ops{env: refEnv{}.Env}
	if got, err := plain.resolveDoneCommit(ctx, "tsk_1", "ABC1234"); err != nil || got != "abc1234" {
		t.Errorf("no git, hex id: %q %v, want abc1234", got, err)
	}
	if _, err := plain.resolveDoneCommit(ctx, "tsk_1", "HEAD"); errs.CodeOf(err) != errs.InvalidInput {
		t.Errorf("no git, HEAD: %v, want E_INVALID_INPUT", err)
	}
}
