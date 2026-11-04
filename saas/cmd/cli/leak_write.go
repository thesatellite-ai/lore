// leak_write.go — the F16 write-time leak check (trial F8, F9). A row
// routed to a client repo (`--to repo`, or a default that lands there) is
// checked BEFORE anything is written, for every kind that routes through
// routeWrite (memory, rule, decision, task, comment, mission, …): a
// refused write prints no success line and leaves no row. Before this, a
// task was created, announced and then dropped by the live write check,
// and a rule file was written with exit 0 and only the commit refused it.
// The later checks (live write, landing, pre-commit, commit-msg, pre-push)
// stay: this one sees only the text handed to routeWrite.
package main

import (
	"context"
	"os"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/leakcheck"
	"saas/pkg/aicoder/orgresolve"
)

// sharedSourceKey marks the context of `lore share`: the write-time check
// allows the text of exactly that row (the copy is meant to carry it).
type sharedSourceKey struct{}

// withSharedSource marks ctx as a share of row id.
func withSharedSource(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sharedSourceKey{}, id)
}

// sharedSource returns the row a share context copies ("" = not a share).
func sharedSource(ctx context.Context) string {
	id, _ := ctx.Value(sharedSourceKey{}).(string) // absent key reads "": not a share
	return id
}

// writeNeedsLeakCheck decides whether a repo write is checked at write
// time: in a client repo, and when lore cannot tell whether this is one
// (fail closed). Every other repo (the org's own, or a plain repo with no
// org) skips it without touching any store, so solo users pay nothing;
// personal notes in a plain repo stay guarded by the commit hooks.
func writeNeedsLeakCheck(s *orgSituation) bool {
	if s == nil {
		return false
	}
	return s.Result.Role == orgresolve.RoleClient || s.Result.LinkUnknown
}

// leakCheckRouteWrite checks the text of a row about to be written to this
// repo. It does nothing for a store or personal target (the client cannot
// read those) or empty text. It fails closed like every F16 check: when
// the store cannot be read and the text is long enough to match, the
// write is refused (E_LEAK_BLOCKED) and nothing is written.
func leakCheckRouteWrite(ctx context.Context, route writeRoute, kind, text string) error {
	if !route.Repo || strings.TrimSpace(text) == "" || !writeNeedsLeakCheck(route.Situation) {
		return nil
	}
	root := route.Situation.Dir
	if root == "" {
		d, err := os.Getwd()
		if err != nil {
			return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
		}
		root = d
	}
	it := leakcheck.Item{Where: "this " + kind, Content: []byte(text)}
	lc := leakContext{action: leakActionWrite, what: "this " + kind}
	if src := sharedSource(ctx); src != "" {
		it.AllowRows = []string{src}
		lc = leakContext{action: leakActionShare, what: "this share", source: src}
	}
	return runLeakCheck(ctx, root, []leakcheck.Item{it}, false, lc)
}

// leakCheckMove is the F16 write-time check of `lore move <id> --to repo`
// (coordinator decision: a move is a write, checked before anything is
// written or asked, fail closed). texts are the moved row's prose fields.
// The row's own text is allowed (moving it is the deliberate transfer, and
// after the move its store copy is a tombstone the index no longer holds);
// text of every OTHER internal row it quotes blocks, as do the id,
// reference and secret rules. Only a client repo (or one whose link status
// is unknown) is checked; dir is the repo root.
func leakCheckMove(ctx context.Context, dir, id string, texts []string) error {
	s, err := gatherOrgSituation(ctx, dir)
	if err != nil {
		return err
	}
	if s.ResolveErr != nil {
		return orgResolveCLIError(s.ResolveErr)
	}
	content := strings.TrimSpace(strings.Join(texts, "\n"))
	if content == "" || !writeNeedsLeakCheck(s) {
		return nil
	}
	it := leakcheck.Item{Where: "move " + id, Content: []byte(content), AllowRows: []string{id}}
	return runLeakCheck(ctx, dir, []leakcheck.Item{it}, false, leakContext{action: leakActionMove, what: "this move", source: id})
}
