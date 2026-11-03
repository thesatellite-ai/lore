// to_flags.go — the shared `--to` / `--everywhere` / `--force-org` / `--yes`
// plumbing for write commands (LORE_TEAM_SPEC §1 Flags, F14, F17).
//
// A write command binds the flags with bindToFlags, then calls
// resolveWriteTarget once before writing. P1 implements the org checks and
// the personal placement (F17); choosing the store project for --to <org>
// and the client-repo defaults (F14) arrive with stores.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/personalplace"

	"github.com/spf13/cobra"
)

// Flag names shared by every write command.
const (
	flagTo         = "to"
	flagEverywhere = "everywhere"
	flagForceOrg   = "force-org"
)

// toFlags holds the shared write-target flags.
type toFlags struct {
	To         string
	Everywhere bool
	ForceOrg   bool
	Yes        bool
	// Create allows `--to <org>/<folder>` to make a store project that
	// exists in no listed store (coordinator Q-stores-2: refused without it,
	// so a typo never creates a new lore). Bound by bindCreateFlag.
	Create bool
}

// bindToFlags adds --to, --everywhere, --force-org and --yes to cmd.
// Commands that already define --yes must not call this twice; pass
// withYes=false and set f.Yes from their own flag.
func bindToFlags(cmd *cobra.Command, f *toFlags, withYes bool) {
	cmd.Flags().StringVar(&f.To, flagTo, "", "where the row goes: repo | <org> | <org>/<folder> | me | me/<org>")
	cmd.Flags().BoolVar(&f.Everywhere, flagEverywhere, false, "with --to me: file the note under everywhere/ (loads in every repo)")
	cmd.Flags().BoolVar(&f.ForceOrg, flagForceOrg, false, "write even though LORE_ORG or lore org use disagrees with a store link")
	if withYes {
		cmd.Flags().BoolVar(&f.Yes, flagYes, false, "confirm without a prompt")
	}
}

// flagCreate is --create on entity add commands.
const flagCreate = "create"

// bindCreateFlag adds --create to an add command that takes --to.
func bindCreateFlag(cmd *cobra.Command, f *toFlags) {
	cmd.Flags().BoolVar(&f.Create, flagCreate, false, "with --to <org>/<folder>: create that store project if it exists nowhere yet")
}

// writeTarget is the decision for one write.
type writeTarget struct {
	Target personalplace.Target
	// Personal is set for --to me / me/<org>: the folder by F17.
	Personal *personalplace.Placement
	// Situation is the org resolution the decision used.
	Situation *orgSituation
}

// resolveWriteTarget parses and checks the flags against the resolved org
// and, for a personal note, places it (F17), asking on a terminal when an
// --everywhere note names a known org. text is the note's title and body.
func resolveWriteTarget(ctx context.Context, f toFlags, text string) (writeTarget, error) {
	t, err := personalplace.ParseTo(f.To)
	if err != nil {
		return writeTarget{}, errcodes.New(errcodes.InvalidInput, err.Error())
	}
	dir, err := os.Getwd()
	if err != nil {
		return writeTarget{}, errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	s, err := gatherOrgSituation(ctx, dir)
	if err != nil {
		return writeTarget{}, err
	}
	if s.ResolveErr != nil {
		return writeTarget{}, orgResolveCLIError(s.ResolveErr)
	}
	org := s.Result.OrgName()
	if err := personalplace.CheckTo(t, org, f.Everywhere); err != nil {
		return writeTarget{}, errcodes.New(errcodes.InvalidInput, err.Error()).
			WithHint("orgs never mix: a repo writes only into the org it resolved to (`lore whoami`)")
	}
	if err := s.Result.CheckWrite(f.ForceOrg, t.Explicit()); err != nil {
		return writeTarget{}, orgResolveCLIError(err)
	}
	wt := writeTarget{Target: t, Situation: s}
	if !t.IsPersonal() {
		return wt, nil
	}
	p, err := personalplace.Place(personalplace.Inputs{
		Org: org, Everywhere: f.Everywhere, Text: text,
		KnownNames: personalplace.KnownNames(s.Config), TTY: stdinIsTTY(), Yes: f.Yes,
	})
	if errors.Is(err, personalplace.ErrEverywhereNeedsConfirm) {
		return writeTarget{}, errcodes.New(errcodes.InvalidInput, err.Error()).
			WithHint("pass --yes to file it under everywhere/ anyway, or drop --everywhere to keep it in this org's folder")
	}
	if err != nil {
		return writeTarget{}, errcodes.New(errcodes.InvalidInput, err.Error())
	}
	if p.NeedsConfirm && !confirmYes(fmt.Sprintf("note mentions %v, a known org or repo. Save it under everywhere/ anyway? [y/N] ", p.Mentions)) {
		return writeTarget{}, errcodes.New(errcodes.InvalidInput, "not saved")
	}
	wt.Personal = &p
	return wt, nil
}
