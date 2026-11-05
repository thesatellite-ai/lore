package main

import (
	"os"
	"path/filepath"
)

// testCoreRoot is the temp folder holding the test binary's package-level
// core repo and home; runTests removes it at the end.
var testCoreRoot string

// _ points coreMountDirs at a temp repo and home before mountCore runs at
// package init (cl-gate): every package-level var initializer runs before
// any init, and this one depends on coreMountDirs, so it runs after that
// var is set to processCoreDirs. Without it a unit test reaching coreApp
// wrote into the checkout's own .lore and read the real ~/.lore.
var _ = useTempCoreDirs()

func useTempCoreDirs() bool {
	coreMountDirs = func() (string, string) {
		root, err := os.MkdirTemp("", "lore-cli-core-")
		if err != nil {
			panic(err) // package init of a test binary: nothing to report to yet
		}
		testCoreRoot = root
		dir, home := filepath.Join(root, "repo"), filepath.Join(root, "home")
		for _, d := range []string{dir, home} {
			if err := os.MkdirAll(d, 0o750); err != nil {
				panic(err)
			}
		}
		return dir, home
	}
	return true
}
