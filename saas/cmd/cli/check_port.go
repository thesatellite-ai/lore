// check_port.go — opscheck.Checker for this binary (P3-CFG-1): the core's
// `lore check leaks|pr-body` operations run the v0.1.17 leak rules here
// (leak index, org resolution, the core scanner, FB-META-1 metadata
// rules). Flow of check.leaks, exactly as the old cobra command:
//
//	--ci        ─► runCheckCI (ids and structure only, no store)
//	--as-client ─► runCheckAsClient (the whole repo as the client sees it)
//	otherwise   ─► [--commit-msg file] + staged files (unless --commit-msg
//	               alone) ─► runLeakCheck (fails closed)
package main

import (
	"context"
	"os"

	"saas/pkg/aicoder/leakcheck"

	"github.com/thesatellite-ai/lore/model/errs"
	opscheck "github.com/thesatellite-ai/lore/ops/check"
	"github.com/thesatellite-ai/lore/wire"
)

// The lines a clean check prints (v0.1.17 text).
const (
	ciClean       = "✓ lore check leaks --ci: every row in .lore/data belongs to this project"
	asClientClean = "✓ lore check leaks --as-client: nothing internal would become visible to the client"
)

// leakPort is opscheck.Checker for this binary.
type leakPort struct{}

// CheckLeaks runs check.leaks in the mode in asks for.
func (leakPort) CheckLeaks(ctx context.Context, in opscheck.LeaksIn) (opscheck.LeaksOut, error) {
	root, err := os.Getwd()
	if err != nil {
		return opscheck.LeaksOut{}, errs.Wrap(errs.Internal, "getwd", err)
	}
	switch {
	case in.CI:
		if err := runCheckCI(ctx, root); err != nil {
			return opscheck.LeaksOut{}, coreErr(err)
		}
		return opscheck.LeaksOut{Message: ciClean}, nil
	case in.AsClient:
		if err := runCheckAsClient(ctx, root, in.Explain); err != nil {
			return opscheck.LeaksOut{}, coreErr(err)
		}
		return opscheck.LeaksOut{Message: asClientClean}, nil
	}
	var items []leakcheck.Item
	if in.CommitMsg != "" {
		it, err := commitMessageItem(ctx, root, in.CommitMsg)
		if err != nil {
			return opscheck.LeaksOut{}, coreErr(err)
		}
		items = append(items, it)
	}
	if in.Staged || in.CommitMsg == "" {
		st, err := stagedItems(ctx, root)
		if err != nil {
			return opscheck.LeaksOut{}, coreErr(err)
		}
		items = append(items, st...)
	}
	if err := runLeakCheck(ctx, root, items, in.Explain, leakContext{action: leakActionCommit, what: "this commit"}); err != nil {
		return opscheck.LeaksOut{}, coreErr(err)
	}
	return opscheck.LeaksOut{}, nil // a hook's check is silent when clean
}

// CheckText checks one text about to be posted (check.pr_body).
func (leakPort) CheckText(ctx context.Context, in opscheck.TextIn) error {
	root, err := os.Getwd()
	if err != nil {
		return errs.Wrap(errs.Internal, "getwd", err)
	}
	items := []leakcheck.Item{{Where: in.Where, Content: in.Body}}
	return coreErr(runLeakCheck(ctx, root, items, in.Explain, leakContext{action: leakActionText, what: "this text"}))
}

// coreLocalPorts are the rules of the core's Local operations in this
// binary (wire.Options.Local, P3-CFG-1).
func coreLocalPorts() wire.LocalPorts {
	return wire.LocalPorts{Config: configSchemaPort{}, Leaks: leakPort{}, Clones: clonePort{}}
}
