package op

import "context"

// confirmedKey carries the caller's confirmation into a Local operation.
type confirmedKey struct{}

// WithConfirmed returns ctx carrying whether the caller already confirmed
// (op.Caller.Yes). The runner of Local operations sets it: a Local
// operation runs without the engine's confirm step, so one that decides
// for itself to ask (Confirm.MayAsk: answer E_NEEDS_CONFIRM, the surface
// asks and runs it again with Yes) reads the answer with Confirmed
// (P3-MISC-2, `lore cache prune`).
func WithConfirmed(ctx context.Context, yes bool) context.Context {
	return context.WithValue(ctx, confirmedKey{}, yes)
}

// Confirmed reports whether ctx carries the caller's confirmation; false
// when the runner set none (fail safe: ask again).
func Confirmed(ctx context.Context) bool {
	yes, ok := ctx.Value(confirmedKey{}).(bool)
	return ok && yes
}
