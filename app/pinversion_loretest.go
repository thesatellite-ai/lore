//go:build loretest

package app

import "os"

// envPinVersion stands in for the release a test build pins the
// lore-sync-merge workflow to (test builds only, like LORE_NOW): a test
// binary is a development build, which never moves the pin, so without it
// no story could watch a dry run leave the workflow alone (devco:D-708).
const envPinVersion = "LORE_TEST_PIN_VERSION"

// pinVersion is version, or LORE_TEST_PIN_VERSION when it is set.
func pinVersion(version string) string {
	if v := os.Getenv(envPinVersion); v != "" {
		return v
	}
	return version
}
