//go:build loretest_format

package row

// FormatVersion in a `loretest_format` build is one above the release:
// the test step below proves the migrate machinery end to end (rule 4)
// without a real format change. Never in a release build.
const FormatVersion = 3

// TestFeature is the test step's feature name.
const TestFeature = "loretest_format"

// testKey is the reserved key a row must carry to need the test step.
const testKey = "_loretest"

// testSteps is the one test-only Additive step: a row with "_loretest"
// is written at _v 3, every other row stays at the release version.
var testSteps = []Step{{
	To: 3, Kind: Additive, Feature: TestFeature,
	Up:    func(r Row) (Row, error) { return r, nil },
	Needs: func(r Row) bool { _, ok := r.Reserved[testKey]; return ok },
}}
