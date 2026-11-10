//go:build !race

package gitleaks

// Outside the race detector the SECRETS 3.8 budgets apply as written and
// a timed case takes the fastest of three runs (budget_test.go).
const (
	budgetSlack = 1
	budgetRuns  = 3
)
