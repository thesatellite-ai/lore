//go:build !loretest

package main

import "context"

// migrateTestHooks never installs a hook in a release build: crash and
// pause points (FORMAT_EVOLUTION 7.8) are a test seam, and an environment
// variable must not be able to kill or stall a real migration.
func migrateTestHooks(ctx context.Context) context.Context { return ctx }
