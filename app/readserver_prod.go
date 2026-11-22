//go:build !loretest

package app

import "time"

// readServerDefault: a release build asks the read server (readserver.go)
// unless LORE_READ_SERVER, CI or read_server: false turn it off.
const readServerDefault = true

// readServerIdleFor is how long the server waits for a call: always
// readServerIdle in a release build.
func readServerIdleFor() time.Duration { return readServerIdle }

// noteReadServer records how a call ran, in test builds only.
func noteReadServer(string) {}

// readServerTestDie makes the server stop mid-command, in test builds only.
func readServerTestDie([]string) string { return readServerDieNo }

// noteServerHeap reports the server's live heap, in test builds only.
func noteServerHeap([]string) {}

// readServerBaseLive replaces the live heap the server may hold beyond
// what it keeps (serverLimits), in test builds only: 0, the default.
func readServerBaseLive() uint64 { return 0 }
