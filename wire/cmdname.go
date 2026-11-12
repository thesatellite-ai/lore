package wire

// cmdname.go — the name of the command a run is, for the lines lore
// prints about what it set up (owner bug 6, 2026-11-12): a line printed
// while `lore member list` ran says "lore member list:", never "lore
// sync:", so the person knows which command did (or would do) it.

import (
	"context"
	"strings"
)

// commandNameKey carries the running command's name on a run's context.
type commandNameKey struct{}

// defaultCommandName is the name when the run's context carries none.
const defaultCommandName = "lore"

// WithCommandName returns ctx naming the running command ("lore member
// list"); an empty name leaves ctx as it is.
func WithCommandName(ctx context.Context, name string) context.Context {
	name = strings.TrimSpace(name)
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, commandNameKey{}, name)
}

// CommandName is the running command's name (WithCommandName), else
// "lore".
func CommandName(ctx context.Context) string {
	if v, _ := ctx.Value(commandNameKey{}).(string); v != "" {
		return v
	}
	return defaultCommandName
}

// CommandPrefix is the prefix of a line the running command prints
// ("lore member list: ").
func CommandPrefix(ctx context.Context) string { return CommandName(ctx) + ": " }

// OpCommandName is the CLI spelling of an operation name ("member.list" →
// "lore member list"), for a run with no CLI command (REST).
func OpCommandName(op string) string {
	return defaultCommandName + " " + strings.ReplaceAll(op, ".", " ")
}
