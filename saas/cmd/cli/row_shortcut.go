// row_shortcut.go — the generic row commands of the 0.1.18 core under
// their short CLI words (CLI_REFERENCE 2.7, 2.13): `lore show <id>` runs
// the registry's `lore row show`, `lore lint` runs `lore lint run`, and
// `lore move <id>` (share_cmd.go) runs row.move for rows of core nouns.
//
//	lore show <id> [--origin] ─► row show (same flags, one implementation)
//	lore lint [--code c]      ─► lint run
//	lore machines [show <id>] ─► machine list | machine show (CLI 3.8)
//	lore trash [--type …]     ─► trash list (CLI 3.9)
//	lore plan apply …         ─► refused, hint: lore mission apply (2.15)
//	lore move <id> --to t     ─► runCoreMove: preview + F16 leak check,
//	                             confirm, then row.move (same id, PROV5)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"saas/pkg/aicoder/errcodes"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opslint "github.com/thesatellite-ai/lore/ops/lint"
	opsplan "github.com/thesatellite-ai/lore/ops/plan"
	opsrow "github.com/thesatellite-ai/lore/ops/row"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/store"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Flags of `lore move` beyond --to / --yes (CLI_REFERENCE 2.7).
const (
	flagDropInternal = "drop-internal"
	flagWithTime     = "with-time"
	rowShowVerb      = "show"
	lintRunVerb      = "run"
	machineListVerb  = "list"
	trashListVerb    = "list"
	planApplyVerb    = "apply"
)

// mountRowShortcuts adds `lore show <id>`, makes bare `lore lint` run
// `lore lint run` and bare `lore machine(s)` run `lore machine list`. Nothing is added when the core commands are missing.
func mountRowShortcuts(root *cobra.Command) {
	if show, _, err := root.Find([]string{opsrow.Noun, rowShowVerb}); err == nil && show.Name() == rowShowVerb && show.RunE != nil {
		root.AddCommand(shortcutOf(show, "show <id>", "Show any row by id (--origin: where it came from)"))
	}
	defaultVerb(root, opslint.Noun, lintRunVerb)
	// CLI_REFERENCE 3.8: `lore machines [--json]` lists the profiles and
	// `lore machines show <m_id>` shows one; the registry noun is
	// `machine`, so the plural is an alias and the bare word runs list.
	if g := defaultVerb(root, opsinfra.NounMachine, machineListVerb); g != nil {
		g.Aliases = append(g.Aliases, opsinfra.NounMachine+"s")
	}
	// CLI_REFERENCE 3.9: bare `lore trash [--type …]` lists the trash.
	defaultVerb(root, opstrash.Noun, trashListVerb)
	// CLI_REFERENCE 2.15 (TSQ9): `lore plan apply` is gone; it answers as
	// an unknown command but names the new spelling.
	if g, _, err := root.Find([]string{opsplan.Noun}); err == nil && g.Name() == opsplan.Noun {
		g.AddCommand(&cobra.Command{
			Use: planApplyVerb, Hidden: true, Args: cobra.ArbitraryArgs, DisableFlagParsing: true,
			RunE: func(_ *cobra.Command, args []string) error {
				return errcodes.New(errcodes.InvalidInput, `unknown command "`+planApplyVerb+`" for "lore `+opsplan.Noun+`"`).
					WithHint(strings.TrimSpace("use lore mission apply " + strings.Join(args, " ")))
			},
		})
	}
}

// defaultVerb makes bare `lore <noun>` run `lore <noun> <verb>` (other
// verbs stay subcommands) and returns the group; nil when either is missing.
func defaultVerb(root *cobra.Command, noun, verb string) *cobra.Command {
	g, _, err := root.Find([]string{noun})
	if err != nil || g.Name() != noun {
		return nil
	}
	run, _, err := g.Find([]string{verb})
	if err != nil || run == g || run.RunE == nil {
		return nil
	}
	g.Short = run.Short
	g.Args = cobra.ArbitraryArgs
	g.DisableFlagParsing = true
	g.RunE = forward(run)
	return g
}

// shortcutOf is a top-level command that parses its arguments with target's
// flags and runs target, so both spellings share one implementation.
func shortcutOf(target *cobra.Command, use, short string) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Long: target.Long, Example: target.Example,
		DisableFlagParsing: true,
		RunE:               forward(target),
	}
}

// forward runs target with args parsed by target's own flag set.
func forward(target *cobra.Command) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		target.SetContext(cmd.Context())
		target.SetOut(cmd.OutOrStdout())
		target.SetErr(cmd.ErrOrStderr())
		if err := target.ParseFlags(args); err != nil {
			// `lore show --help` (CLI_REFERENCE 2.13): pflag answers -h with
			// ErrHelp, which is a request for the target's help, not an error.
			if errors.Is(err, pflag.ErrHelp) {
				return target.Help()
			}
			return target.FlagErrorFunc()(target, err)
		}
		if h, _ := target.Flags().GetBool("help"); h {
			return target.Help()
		}
		rest := target.Flags().Args()
		if err := target.ValidateArgs(rest); err != nil {
			return err
		}
		return target.RunE(target, rest)
	}
}

// runCoreMove is `lore move <id> --to <store>` for a row of a core table:
// show the text and where it goes, the F16 leak check into this repo
// (a move is a write), confirm (agents may confirm too: AGENTS OVERRIDE,
// OQ25), then row.move, which keeps the id (PROV5, XQ5) and refuses
// secret references (BATCH 5) and internal metadata without
// --drop-internal (MQ11) into a client repo.
func runCoreMove(ctx context.Context, table row.Table, id string, f *shareFlags) error {
	if coreApp == nil {
		return errcodes.New(errcodes.Internal, "the 0.1.18 core is not mounted")
	}
	to := f.to.To
	if to == "" {
		return errcodes.New(errcodes.InvalidInput, "--to is required (a store name from .lore/stores.yaml, or repo)")
	}
	if f.withTime != "" && !slices.Contains(engine.TimeShares(), f.withTime) {
		return errcodes.New(errcodes.InvalidInput, "--with-time takes "+strings.Join(engine.TimeShares(), " or ")+"; nothing written")
	}
	rid, err := ids.ParseAny(id)
	if err != nil {
		return errs.Wrap(errs.InvalidID, id+" is not an id", err)
	}
	_, r, err := coreApp.Get(ctx, engine.Ref{Table: table, ID: rid})
	if err != nil {
		return err
	}
	var texts []string
	for _, name := range leakTextFields {
		if s := renderField(r, name); s != "" {
			texts = append(texts, s)
		}
	}
	if store.Source(to) == store.SourceRepo {
		cwd, werr := os.Getwd()
		if werr != nil {
			return errcodes.New(errcodes.Internal, "getwd").WithCause(werr)
		}
		if lerr := leakCheckMove(ctx, cwd, id, texts); lerr != nil {
			return lerr
		}
	}
	preview := ""
	if len(texts) > 0 {
		preview = texts[0]
	}
	// --json keeps stdout for the result alone (CLI_REFERENCE 2.7).
	previewOut := os.Stdout
	if f.asJSON {
		previewOut = os.Stderr
	}
	fmt.Fprintf(previewOut, "This becomes visible to everyone with access to store %s:\n  %q\n", to, oneLine(preview))
	if !f.yes {
		if !stdinIsTTY() {
			return errcodes.New(errcodes.InvalidInput, "not moved: confirm on a terminal (or --yes); nothing written")
		}
		if !confirmYes("Move it? [y/N] ") {
			fmt.Println("nothing written")
			return nil
		}
	}
	if gerr := ensureCoreGitignore(ctx); gerr != nil {
		return coreErr(gerr)
	}
	e, ok := coreApp.Registry().Lookup(opsrow.OpMove)
	if !ok {
		return errcodes.New(errcodes.Internal, opsrow.OpMove+" is not registered")
	}
	in, err := json.Marshal(opsrow.MoveIn{ID: id, To: to, DropInternal: f.dropInternal, WithTime: f.withTime, WithChildren: f.withChildren})
	if err != nil {
		return errcodes.New(errcodes.Internal, "encode the move").WithCause(err)
	}
	res, err := coreApp.Run(ctx, e, op.JSONDecoder{Body: in}, op.Caller{Interactive: stdinIsTTY(), Yes: true})
	if err != nil {
		return err
	}
	raw, err := res.JSON()
	if err != nil {
		return err
	}
	if f.asJSON {
		fmt.Println(string(raw))
		return nil
	}
	var out opsrow.MoveOut
	if uerr := json.Unmarshal(raw, &out); uerr != nil {
		return errcodes.New(errcodes.Internal, "read the move result").WithCause(uerr)
	}
	fmt.Print(out.Text())
	return nil
}
