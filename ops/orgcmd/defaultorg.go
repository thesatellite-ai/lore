package orgcmd

// defaultorg.go — the org an org verb works on when none is named (owner
// run 2026-11-12: `lore org refresh` alone answered "missing org"). Only
// the verbs where the default is unambiguous and the verb shows its plan
// or changes only this machine take it: refresh, link and unlink (both
// list their plan and ask first). create, import and rename name a new or
// renamed entry, and set writes a committed file, so they keep requiring
// the name.
//
//	org <verb> without <org> ─▶ Host upgraded to DefaultOrger?
//	    ─ no  ─▶ "name the org entry: lore org <verb> <org>"
//	    ─ yes ─▶ the only org entry on this machine
//	           ├▶ else the active setup's org
//	           └▶ else refused, naming every org entry and the exact command
//	    ─▶ the verb runs on it; its report starts with the line saying which
//	       org and why

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// DefaultOrger is the optional Host upgrade that picks the org entry of a
// verb run without one. why says how it was picked ("the only org entry
// on this machine", "the active setup lead's org"); a refusal names every
// org entry and the exact command to run.
type DefaultOrger interface {
	DefaultOrg(ctx context.Context, verb string) (name, why string, err error)
}

// withDefaultOrg runs run with *org filled in from the host's default when
// it is empty, and starts the report with the line naming that default.
func withDefaultOrg(ctx context.Context, h Host, name string, org *string, run func() (Report, error)) (Report, error) {
	if strings.TrimSpace(*org) != "" {
		return run()
	}
	verb := strings.ReplaceAll(name, ".", " ")
	d, ok := upgrade.As[DefaultOrger](h)
	if !ok {
		return Report{}, errs.New(errs.InvalidInput, "name the org entry: lore "+verb+" <org>")
	}
	picked, why, err := d.DefaultOrg(ctx, verb)
	if err != nil {
		return Report{}, err
	}
	*org = picked
	r, err := run()
	if err != nil {
		return r, err
	}
	r.Lines = append([]string{"org " + picked + " (" + why + ")"}, r.Lines...)
	return r, nil
}
