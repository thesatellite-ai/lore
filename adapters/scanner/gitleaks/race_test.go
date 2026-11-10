//go:build race

package gitleaks

// Under the race detector, which slows regexp-heavy code by roughly an
// order of magnitude, the SECRETS 3.8 budgets scale by budgetSlack and a
// timed case runs once (budget_test.go); the normal build pins the
// budgets as written.
const (
	budgetSlack = 15
	budgetRuns  = 1
)
