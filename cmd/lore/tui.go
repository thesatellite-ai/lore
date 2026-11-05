package main

// tui.go — `lore tui` of the standalone binary: the read-only run-time
// browser of surface/tui over the engine (every attached store, table
// definitions at run time; FA-OLDCODE-3, TUI-RT). `--kind` opens on one
// table first, by table name or its v0.1.17 singular (P3-TUI-1).

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/surface/tui"
)

// engineSource adapts the engine to tui.Source.
type engineSource struct{ eng *engine.Engine }

func (s engineSource) Tables() []schema.TableDef { return s.eng.Registry().Tables() }
func (s engineSource) Stores() []store.Source    { return s.eng.Stores() }
func (s engineSource) List(ctx context.Context, src store.Source, t row.Table) ([]row.Row, error) {
	return s.eng.List(ctx, src, t)
}

// tuiCommand is `lore tui [--kind <table>]`.
func (h *host) tuiCommand() *cobra.Command {
	var opts tui.Options
	cmd := &cobra.Command{
		Use:     "tui",
		Short:   "Browse every table of the attached stores in the terminal (read-only)",
		Example: "  lore tui\n  lore tui --kind decision",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			eng, err := h.app.Engine(cmd.Context())
			if err != nil {
				return err
			}
			return tui.Run(cmd.Context(), engineSource{eng: eng}, cmd.InOrStdin(), cmd.OutOrStdout(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Kind, "kind", "", "table to open first (e.g. memory, task, decision); default the tables screen")
	return cmd
}
