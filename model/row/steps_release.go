//go:build !loretest_format

package row

// FormatVersion is the newest "_v" this binary reads and writes
// (len(steps)+1; TestStepsWellFormed pins it). v0.1.17 wrote 1. A reader
// refuses a higher number (E_OLD_LORE) and upcasts lower ones in memory.
const FormatVersion = 2

// testSteps is empty in a release build (TestReleaseHasNoTestSteps).
var testSteps []Step
