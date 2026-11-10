// Package repoguard holds tests that guard the repository itself rather
// than the CLI's behaviour: the repo's own git hooks (.githooks/pre-push),
// run for real against scratch repositories. It has no non-test code
// beyond this comment; nothing imports it.
//
// Ported from saas/cmd/cli/prepush_hook_test.go (B11) so the guard
// survives the old module's deletion (phase E).
package repoguard
