package cli

import (
	"context"
	"io"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// byActorID is an actor id where a human line names who did something:
// "created … by act_<32 hex>", "deleted by act_…". A show's writers line
// (ops/internal/auditrec) is built from the record alone, which holds ids
// for --json and scripts; the person reading the text wants the name
// (bug hunt B3-24: `lore show` printed "by act_2a91…" while trash list
// printed "Nina Newcomer").
var byActorID = regexp.MustCompile(`\bby (act_[0-9a-f]{32})\b`)

// WithActorNamer injects the lookup that names an actor id in human
// output: the @handle, else the display name, else the id itself. The
// binary passes the app's namer; without one every id is printed as is.
// It is called only when an output holds an actor id after "by".
func WithActorNamer(f func(context.Context) func(string) string) Option {
	return func(c *config) { c.actorNamer = f }
}

// nameActors is text with each "by act_…" replaced by the actor's name
// (one lookup per run; an id the namer cannot name stays). --json output
// never goes through it.
func (c config) nameActors(ctx context.Context, text string) string {
	if c.actorNamer == nil || !byActorID.MatchString(text) {
		return text
	}
	name := c.actorNamer(ctx)
	return byActorID.ReplaceAllStringFunc(text, func(m string) string {
		id := byActorID.FindStringSubmatch(m)[1]
		if n := name(id); n != "" {
			return "by " + n
		}
		return m
	})
}

// printNamedError is printError for a generated command, with the human
// line's actor ids named like its output ("revoked by act_…" in
// E_LOST_CLAIM, "deleted by act_…" in E_IN_TRASH; bug hunt D-1). The
// --json envelope goes to stdout untouched, ids and all.
func (c config) printNamedError(cmd *cobra.Command, err error, asJSON bool) int {
	if asJSON {
		return printError(cmd.OutOrStdout(), cmd.ErrOrStderr(), err, true, c.color(StreamErr), cmd.CommandPath())
	}
	var line strings.Builder
	code := printError(cmd.OutOrStdout(), &line, err, false, c.color(StreamErr), cmd.CommandPath())
	_, _ = io.WriteString(cmd.ErrOrStderr(), c.nameActors(cmd.Context(), line.String()))
	return code
}
