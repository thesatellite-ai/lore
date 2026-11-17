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

// interactiveKey carries whether a person at a terminal can answer now.
type interactiveKey struct{}

// WithInteractive returns ctx carrying op.Caller.Interactive for a Local
// operation. Why (devco D-526): a Local operation skips engine.Run, so
// engine.Interactive cannot answer there; guided setup's own: offer asks
// in a terminal and, without one, only names the command.
func WithInteractive(ctx context.Context, interactive bool) context.Context {
	return context.WithValue(ctx, interactiveKey{}, interactive)
}

// Interactive reports whether ctx carries an interactive caller; false
// when the runner set none (fail safe: never wait on a prompt).
func Interactive(ctx context.Context) bool {
	v, ok := ctx.Value(interactiveKey{}).(bool)
	return ok && v
}
