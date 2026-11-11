// Package e2eenv holds what every end-to-end harness (tests/teame2e/teamharness, which the
// user scenarios and the journeys use, tests/machines and tests/setupe2e) hands on to the lore
// processes it starts beyond each world's own environment: the Go runtime settings of
// LORE_E2E_GOENV.
//
// Why: the gate starts about 100,000 lore processes, each living a fraction of a second. Go
// sizes a process for every core of the machine, so on the 48-core gate server each spent
// about 40 % of its CPU on garbage-collector workers and idle scheduler threads, and the gate
// as a whole was bound by CPU. Taskfile.lore.yml sets LORE_E2E_GOENV (GOMAXPROCS, GOGC,
// GOMEMLIMIT; measured there). The settings change what a run costs, never what lore does,
// and only lore processes get them: the Go toolchain and the test binaries keep Go's
// defaults (a GOMEMLIMIT sized for lore would make the linker, at ~700 MB, collect garbage
// without end). Unset, lore runs with Go's defaults.
package e2eenv

import (
	"os"
	"strings"
)

// Var names the environment variable that carries the settings: space-separated NAME=VALUE
// pairs, e.g. "GOMAXPROCS=2 GOGC=off GOMEMLIMIT=512MiB".
const Var = "LORE_E2E_GOENV"

// allowed are the only names Var may set: Go runtime settings that change cost, not behaviour.
// Anything else in Var is ignored, so the variable can never change what a test checks.
var allowed = map[string]bool{"GOMAXPROCS": true, "GOGC": true, "GOMEMLIMIT": true}

// LoreGoEnv returns the NAME=VALUE pairs of Var to add to a lore process's environment
// (nil when Var is unset or empty).
func LoreGoEnv() []string {
	var env []string
	for _, kv := range strings.Fields(os.Getenv(Var)) {
		k, v, ok := strings.Cut(kv, "=")
		if ok && v != "" && allowed[k] {
			env = append(env, k+"="+v)
		}
	}
	return env
}
