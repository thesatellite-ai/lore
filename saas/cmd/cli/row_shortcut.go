// row_shortcut.go — the generic row commands of the 0.1.18 core under
// their short CLI words (CLI_REFERENCE 2.7, 2.13): `lore show <id>` runs
// the registry's `lore row show`, `lore lint` runs `lore lint run`, and
// `lore move <id>` (share_cmd.go) runs row.move for rows of core nouns.
//
//	lore show <id> [--origin] ─► row show (same flags, one implementation)
//	lore lint [--code c]      ─► lint run
//	lore move <id> --to t     ─► runCoreMove: preview + F16 leak check,
//	                             confirm, then row.move (same id, PROV5)
package main

import (
	"context"
	"encoding/json"
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
	opslint "github.com/thesatellite-ai/lore/ops/lint"
	opsrow "github.com/thesatellite-ai/lore/ops/row"
	"github.com/thesatellite-ai/lore/store"

	"github.com/spf13/cobra"
)

// Flags of `lore move` beyond --to / --yes (CLI_REFERENCE 2.7).
const (
	flagDropInternal = "drop-internal"
	flagWithTime     = "with-time"
	rowShowVerb      = "show"
	lintRunVerb      = "run"
)

// mountRowShortcuts adds `lore show <id>` and makes bare `lore lint` run
// `lore lint run`. Nothing is added when the core commands are missing.
func mountRowShortcuts(root *cobra.Command) {
	if show, _, err := root.Find([]string{opsrow.Noun, rowShowVerb}); err == nil && show.Name() == rowShowVerb && show.RunE != nil {
		root.AddCommand(shortcutOf(show, "show <id>", "Show any row by id (--origin: where it came from)"))
	}
	g, _, err := root.Find([]string{opslint.Noun})
	if err != nil || g.Name() != opslint.Noun {
		return
	}
	run, _, err := g.Find([]string{lintRunVerb})
	if err != nil || run == g || run.RunE == nil {
		return
	}
	g.Short = run.Short
	g.Args = cobra.ArbitraryArgs
	g.DisableFlagParsing = true
	g.RunE = forward(run)
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
	fmt.Printf("This becomes visible to everyone with access to store %s:\n  %q\n", to, oneLine(preview))
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
	in, err := json.Marshal(opsrow.MoveIn{ID: id, To: to, DropInternal: f.dropInternal, WithTime: f.withTime})
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
	var out opsrow.MoveOut
	if uerr := json.Unmarshal(raw, &out); uerr != nil {
		return errcodes.New(errcodes.Internal, "read the move result").WithCause(uerr)
	}
	fmt.Print(out.Text())
	return nil
}
