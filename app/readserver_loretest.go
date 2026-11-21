//go:build loretest

package app

import (
	"os"
	"slices"
	"time"
)

// readServerDefault: a test build runs every command one-shot unless a
// test sets LORE_READ_SERVER=1, so a suite starts no server it did not ask
// for (readserver.go).
const readServerDefault = false

// Test seams of the read server (test builds only, like LORE_NOW):
const (
	// envReadServerIdle shortens the idle exit (a Go duration).
	envReadServerIdle = "LORE_TEST_READ_SERVER_IDLE"
	// envReadServerLog names a file the CLI appends one line to per call
	// it offered the server: "served" or "one-shot".
	envReadServerLog = "LORE_TEST_READ_SERVER_LOG"
	// envReadServerDie in a client's environment makes the server exit
	// mid-call: 1 after it accepted the call, before it runs it; printing
	// after it printed the start of an answer (a server killed mid-print).
	envReadServerDie = "LORE_TEST_READ_SERVER_DIE"
)

// readServerIdleFor is LORE_TEST_READ_SERVER_IDLE when set, else
// readServerIdle.
func readServerIdleFor() time.Duration {
	if d, err := time.ParseDuration(os.Getenv(envReadServerIdle)); err == nil && d > 0 {
		return d
	}
	return readServerIdle
}

// noteReadServer appends how a call ran to LORE_TEST_READ_SERVER_LOG.
func noteReadServer(how string) {
	p := os.Getenv(envReadServerLog)
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.WriteString(how + "\n")
	_ = f.Close()
}

// readServerTestDie is how the client asked the server to die mid-call.
func readServerTestDie(env []string) string {
	switch {
	case slices.Contains(env, envReadServerDie+"=1"):
		return readServerDieBefore
	case slices.Contains(env, envReadServerDie+"="+readServerDiePrinting):
		return readServerDiePrinting
	}
	return readServerDieNo
}
