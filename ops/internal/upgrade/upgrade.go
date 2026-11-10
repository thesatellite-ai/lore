// Package upgrade is the one place an op checks whether its Env offers an
// optional capability (opclock.Clocker, opsmemory.Labeler, …).
//
// Why it exists: lore prefers optional interface upgrades over required
// configuration, so a surface or a test Env is never forced to implement a
// method it has no use for. Checking for an upgrade is a Go type assertion,
// which needs an interface value; the ~15 ops packages each have their own
// Env interface type, and Go cannot name "any interface type" except as the
// empty interface. Keeping that single forced boundary here (allowlisted in
// internal/layers' TestNoAny with this reason) lets every caller stay typed:
// they pass their own Env and get the capability back typed, or ok=false.
package upgrade

// As reports whether v implements T and returns it typed. v is the caller's
// Env (an interface value); the parameter is the empty interface only
// because the assertion below needs an interface operand — it is narrowed
// to T on the same line and never stored or passed on untyped.
func As[T any](v any) (T, bool) {
	t, ok := v.(T)
	return t, ok
}
