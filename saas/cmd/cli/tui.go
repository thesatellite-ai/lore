// tui.go — `lore tui`
//
// Launches the enttui terminal UI over the SAME .lore/lore.db ent
// client every other subcommand uses (resolved via resolveContext). No
// separate binary, no --db plumbing: it's just another subcommand that
// happens to render a schema-driven TUI instead of printing.
//
// All per-entity wiring is GENERATED into saas/pkg/aicoder/tuigen by the
// enttui codegen CLI (see that package's doc.go) — adding a field or a
// new entity to dbent/schema + `go generate` is the only step; this file
// never changes. (This replaced the old hand-rolled adapter that listed
// every entity by hand.)
//
// Removal recipe:
//  1. rm -rf saas/pkg/aicoder/tuigen
//  2. rm saas/cmd/cli/tui.go
//  3. remove rootCmd.AddCommand(buildTUICommand()) in main.go
package main

import (
	"context"

	"saas/pkg/aicoder/tuigen"

	enttuirt "github.com/khanakia/entx/enttui/runtime"

	"github.com/spf13/cobra"
)

type tuiFlags struct {
	commonFlags
	kind string
	view string
}

// buildTUICommand returns the wired-up `tui` subcommand. Called from main.go.
func buildTUICommand() *cobra.Command {
	f := &tuiFlags{}
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Browse the knowledge DB in an interactive terminal UI",
		Long: `tui opens the current project's .lore/lore.db in an
interactive, schema-driven terminal UI (powered by enttui)

It reuses the exact same ent client + project scoping as every other
lore subcommand — no extra flags or DB path needed. Every dbent
entity is browsable; navigate with vim keys, press ? for the full
keymap and , for the leader menu.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(cmd.Context(), f)
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringVar(&f.kind, "kind", "memory", "kind to open first (e.g. memory, task, decision)")
	cmd.Flags().StringVar(&f.view, "view", "table", "initial view mode: table | list")
	return cmd
}

func runTUI(ctx context.Context, f *tuiFlags) error {
	rctx, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		return err
	}
	defer client.Close()

	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}

	app := enttuirt.New()
	// Generic scope key — the generated Fetch closures read
	// opts.Scope["project_id"] (codegen ran with --scope project_id), so
	// every view is automatically filtered to the resolved project.
	app.SetScope("project_id", projectID)
	app.SetDefaultViewMode(f.view)
	app.SetInitialKind(f.kind)
	tuigen.RegisterAll(app, client)
	// Synthetic non-ent screen: all-tables overview (see tui_tables.go)
	registerTablesScreen(app, rctx.DBPath)

	return app.Run()
}
