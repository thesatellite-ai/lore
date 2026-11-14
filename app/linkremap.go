package app

// linkremap.go — the post-rewrite hook's commit-link remap (H-8): git
// hands the hook the old → new sha of every commit a rebase or an amend
// rewrote, and link.remap moves each commit link to its new sha. It runs
// through the runner, as a command would (live rows, leak checks).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/thesatellite-ai/lore/op"
	opslink "github.com/thesatellite-ai/lore/ops/link"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// linkRemapHook runs link.remap for the rewritten pairs; it never fails the
// hook: a refusal is one stderr line naming the command to run by hand.
func linkRemapHook(r *hosthooks.Runner) hosthooks.RewriteRemap {
	return func(ctx context.Context, pairs []string) {
		e, ok := r.App().Registry().Lookup(opslink.OpRemap)
		if !ok {
			return
		}
		body, err := json.Marshal(opslink.RemapIn{Pairs: pairs})
		if err != nil {
			return
		}
		res, err := r.Run(ctx, e, op.JSONDecoder{Body: body}, op.Caller{Yes: true})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "lore: commit links still name the commits before the rewrite: "+err.Error()+" (run: lore link remap --pair <old>=<new>)") // a hook: stderr is all there is
			return
		}
		out, ok := res.Value().Interface().(opslink.RemapOut)
		if !ok || out.Count == 0 {
			return
		}
		lines := make([]string, 0, len(out.Data))
		for _, m := range out.Data {
			lines = append(lines, m.EntityID)
		}
		_, _ = fmt.Fprintf(os.Stderr, "lore: moved %d commit link(s) to the rewritten commits (%s); commit .lore/data with your next change\n", out.Count, strings.Join(lines, ", ")) // a hook: stderr is all there is
	}
}
