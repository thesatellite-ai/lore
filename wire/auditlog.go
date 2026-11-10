package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	opsaudit "github.com/thesatellite-ai/lore/ops/audit"
)

// `lore audit log|verify` (ops/audit) read the hash-chained audit log. The
// chain is kept by the repo audit sink (Options.Audit): the sink that
// appends it also reads and verifies it, so a sink implementing
// opsaudit.Env answers both. A sink without a chain (the append-only
// AuditFile fallback) has nothing to verify: E_NOT_IMPLEMENTED, never a
// fake "intact" (P3-AUDIT-2).

var _ opsaudit.Env = (*App)(nil)

// chainReader is the repo audit sink as a chain reader, or nil.
func (a *App) chainReader() opsaudit.Env {
	if a.opts.Audit == nil {
		return nil
	}
	if c, ok := a.opts.Audit.(opsaudit.Env); ok {
		return c
	}
	return nil
}

// errNoChain answers an audit read when no sink keeps a hash chain.
func errNoChain() error {
	return errs.New(errs.NotImplemented, "this lore build keeps no hash-chained audit log for this repo").
		WithHint("the append-only write record is " + AuditFile)
}

// AuditLog implements opsaudit.Env.
func (a *App) AuditLog(ctx context.Context, limit int) ([]opsaudit.Entry, error) {
	c := a.chainReader()
	if c == nil {
		return nil, errNoChain()
	}
	return c.AuditLog(ctx, limit)
}

// AuditVerify implements opsaudit.Env.
func (a *App) AuditVerify(ctx context.Context) (opsaudit.Report, error) {
	c := a.chainReader()
	if c == nil {
		return opsaudit.Report{}, errNoChain()
	}
	return c.AuditVerify(ctx)
}
