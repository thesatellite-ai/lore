package cli

import (
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// flagAsValue refuses a value flag whose value is another flag of the
// command (devco:Q-7). pflag hands a value flag the next word whatever it
// is, so `--id --body x` (an empty shell variable after --id) set --id to
// "--body" and the decoder then blamed x as "--id is given twice". The
// cause is a missing value: say so, name the flag the parser took, and how
// to give it. A value that only starts with "--" but names no flag of the
// command (a body text such as "--dry-run is broken") stays a value, and so
// does one glued on with "=" (`--title=--json`, devco:D-326): written that
// way it cannot be a missing value, which is why the refusal names the form.
// raw is the process arguments (nil when unknown: nothing counts as glued).
func flagAsValue(cmd *cobra.Command, raw []string, values map[string]*rawFlag) *errs.Error {
	glued := gluedValues(cmd, raw)
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
			if other == "" || cmd.Flags().Lookup(other) == nil || slices.Contains(glued[rf], v) {
				continue
			}
			return errs.New(UsageCode, "missing value for --"+name+": the next word is the flag --"+other).
				WithHint("give --" + name + " a value, as --" + name + " \"value\" (an empty shell variable after --" + name + " leaves it without one); a value that starts with -- is written --" + name + "=" + v)
		}
	}
	return nil
}

// gluedValues is every value typed in one word with its flag (`--title=x`,
// `-t=x`) before a "--" terminator, keyed by the flag's value so an alias
// spelling counts for its field.
func gluedValues(cmd *cobra.Command, raw []string) map[*rawFlag][]string {
	out := map[*rawFlag][]string{}
	for _, arg := range raw {
		if arg == "--" {
			break
		}
		spec, val, ok := strings.Cut(arg, "=")
		if !ok {
			continue
		}
		var fl *pflag.Flag
		if long, isLong := strings.CutPrefix(spec, "--"); isLong {
			fl = cmd.Flags().Lookup(long)
		} else if short, isShort := strings.CutPrefix(spec, "-"); isShort && len(short) == 1 {
			fl = cmd.Flags().ShorthandLookup(short)
		}
		if fl == nil {
			continue
		}
		if rf, isRaw := fl.Value.(*rawFlag); isRaw {
			out[rf] = append(out[rf], val)
		}
	}
	return out
}
