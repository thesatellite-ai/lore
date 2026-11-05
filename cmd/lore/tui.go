package main

// tui.go — `lore tui` of the standalone binary: the read-only run-time
// browser of surface/tui over the engine (every attached store, table
// definitions at run time; FA-OLDCODE-3). The release binary's `lore tui`
// stays on the v0.1.17 lsync bridge (Q-TUI a) until saas is deleted.

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

// tuiCommand is `lore tui`.
func (h *host) tuiCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Browse every table of the attached stores in the terminal (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			eng, err := h.app.Engine(cmd.Context())
			if err != nil {
				return err
			}
			return tui.Run(cmd.Context(), engineSource{eng: eng}, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}
