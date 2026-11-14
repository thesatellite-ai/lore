package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// flagAsValue refuses a value flag whose value is another flag of the
// command (devco:Q-7). pflag hands a value flag the next word whatever it
// is, so `--id --body x` (an empty shell variable after --id) set --id to
// "--body" and the decoder then blamed x as "--id is given twice". The
// cause is a missing value: say so, name the flag the parser took, and how
// to give it. A value that only starts with "--" but names no flag of the
// command (a body text such as "--dry-run is broken") stays a value.
func flagAsValue(cmd *cobra.Command, values map[string]*rawFlag) *errs.Error {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names) // one stable report when two flags lack a value
	for _, name := range names {
		rf := values[name]
		if rf.typ == op.TypeBool || !cmd.Flags().Changed(name) {
			continue
		}
		for _, v := range rf.vals {
			other, ok := strings.CutPrefix(v, "--")
			if !ok {
				continue
			}
			other, _, _ = strings.Cut(other, "=")
			if other == "" || cmd.Flags().Lookup(other) == nil {
				continue
			}
			return errs.New(UsageCode, "missing value for --"+name+": the next word is the flag --"+other).
				WithHint("give --" + name + " a value, as --" + name + " \"value\" (an empty shell variable after --" + name + " leaves it without one)")
		}
	}
	return nil
}
