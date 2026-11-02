// cache_cmd.go — `lore cache prune` (§1 Commands, side effect 40): remove
// the external caches (~/.lore/cache/<project-id>.db) and internal renders
// (~/.lore/render/<project-id>.md) of projects not used for a while. Both
// are derived: the next command in that project rebuilds them.
package main

import (
	"fmt"
	"path/filepath"
	"time"

	"saas/pkg/aicoder/cachedb"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/stores"

	"github.com/spf13/cobra"
)

// defaultCacheIdle is how long a project's cache may go unused before
// prune removes it (side effect 40: 90 days).
const defaultCacheIdle = 90 * 24 * time.Hour

// flagOlderThan sets the idle threshold.
const flagOlderThan = "older-than"

func newCacheCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Maintain lore's caches outside the checkout (~/.lore/cache, ~/.lore/render)",
	}
	cmd.AddCommand(newCachePruneCommand())
	return cmd
}

func newCachePruneCommand() *cobra.Command {
	var olderThan string
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove caches and internal renders of projects not used for --older-than (default 90d)",
		Example: `  lore cache prune
  lore cache prune --older-than 30d`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			idle := defaultCacheIdle
			if olderThan != "" {
				d, err := lorecfg.ParseDuration(olderThan)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "--older-than: "+err.Error())
				}
				idle = d.D()
			}
			home, err := loreHome()
			if err != nil {
				return err
			}
			res, err := cachedb.Prune(cmd.Context(), filepath.Join(home, stores.CacheDir), filepath.Join(home, stores.RenderDir), cliClock().Now(), idle)
			if err != nil {
				return errcodes.New(errcodes.Internal, "prune caches").WithCause(err)
			}
			for _, pid := range res.Removed {
				fmt.Printf("✓ removed the cache and render of %s\n", pid)
			}
			fmt.Printf("%d removed, %d kept (used within %s)\n", len(res.Removed), res.Kept, idle)
			return nil
		},
	}
	cmd.Flags().StringVar(&olderThan, flagOlderThan, "", "idle time before a cache is removed (e.g. 30d, 2160h); default 90d")
	return cmd
}
