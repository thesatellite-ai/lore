package op

import "context"

// setupKey carries the setup the caller named (ONBOARDING_DESIGN 3.3,
// 7.1): the global `--setup <name>` on the CLI, `X-Lore-Setup` on REST.
// It is a context value, not an input field, because it is global like
// --org: a per-op `--setup` flag would clash with the global one.
type setupKey struct{}

// WithSetup returns ctx carrying the setup name the caller gave ("" =
// none; LORE_SETUP and folder matching then decide, in wire).
func WithSetup(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, setupKey{}, name)
}

// SetupOf is the setup name the caller gave, "" when none.
func SetupOf(ctx context.Context) string {
	s, _ := ctx.Value(setupKey{}).(string)
	return s
}
