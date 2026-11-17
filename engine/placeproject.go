package engine

import "errors"

// ErrNoPlaceProject is in the chain of the refusal a read gets from
// Env.Project where the place it runs at has no project of its own: the
// root of an org store's clone holds every folder's rows but no project
// row, and a read never makes one (devco T-11).
//
// Why it exists: devco D-505. memory, rule, decision and task list read
// every project's rows when no --project is given, so they answered at a
// store root, while the content nouns' lists and searches, link, tag and
// repo list asked Env.Project for "this repo's project" first and were
// refused there. A read that keeps one project's rows tests for this error
// (EveryProject) and reads every project's rows instead, the same answer
// memory list gives at that place. Its text is the phrase the refusal's
// own message carries, so the CLI line prints it once (errs.Error).
var ErrNoPlaceProject = errors.New("has no project row yet")

// EveryProject reports whether err is the refusal of Env.Project at a
// place with no project of its own (ErrNoPlaceProject): a read that
// filters by the current project then keeps every project's rows.
func EveryProject(err error) bool {
	return errors.Is(err, ErrNoPlaceProject)
}
