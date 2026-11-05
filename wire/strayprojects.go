package wire

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// strayProjectNotes is one notice per attached store folder that holds
// project rows besides the one its _meta.json names: duplicates an older
// lore wrote when two clones created the folder at once, each under a
// random id. Commands keep working because the folder's project resolves
// through _meta.json (rosterProject); the notice says which row is used
// and which are ignored, once per open, as v0.1.17's warnStrayProjectRows
// did. The repo store is left out (App.Project resolves its own several
// projects), and so is a folder without a folder identity (the store root)
// or whose _meta.json row is not there (nothing to say "using" about). A
// folder that cannot be read is skipped: the notice is advice, and the
// command that reads the folder reports the error.
func strayProjectNotes(ctx context.Context, st *state) []string {
	names := make([]store.Source, 0, len(st.byName))
	for n := range st.byName {
		if n != store.SourceRepo {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	var out []string
	for _, n := range names {
		pid, err := folderProjectIDOf(ctx, st, n)
		if err != nil || pid.IsZero() {
			continue
		}
		rows, err := st.eng.List(ctx, n, schema.TableProjects)
		if err != nil {
			continue
		}
		var stray []string
		own := false
		for _, r := range rows {
			switch {
			case r.Deleted:
			case r.ID == pid.Any():
				own = true
			default:
				stray = append(stray, r.ID.String())
			}
		}
		if !own || len(stray) == 0 {
			continue
		}
		slices.Sort(stray)
		out = append(out, fmt.Sprintf("warning: %s holds %d extra project row(s) (%s) from an older lore; using %s (its _meta.json project_id)",
			n, len(stray), strings.Join(stray, ", "), pid))
	}
	return out
}
