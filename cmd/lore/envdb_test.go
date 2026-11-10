package main

import (
	"strings"
	"testing"
)

// TestLoreDBEnvRefused (E-GAPS-1 S1): LORE_DB set refuses a row command
// with E_UNSUPPORTED (exit 23) naming the variable, as --db is refused
// (docs/reference/environment.md, flags.md); it is never silently ignored.
// A command with no --db (version) does not care, and with the variable
// unset the same row command works.
func TestLoreDBEnvRefused(t *testing.T) {
	envScopeRepo(t)
	t.Setenv(envLoreDB, "/nonexistent/lore.db")
	code, out, errOut := runCaptured(t, "memory", "list")
	if code != 23 || !strings.Contains(errOut, "[E_UNSUPPORTED] "+envLoreDB) || out != "" {
		t.Fatalf("LORE_DB set: exit %d, stdout %q, stderr %q; want exit 23 E_UNSUPPORTED naming %s", code, out, errOut, envLoreDB)
	}
	if code, _, errOut := runCaptured(t, "version"); code != 0 {
		t.Fatalf("LORE_DB set, lore version: exit %d %s", code, errOut)
	}
	t.Setenv(envLoreDB, "")
	if code, _, errOut := runCaptured(t, "memory", "list"); code != 0 {
		t.Fatalf("LORE_DB empty: exit %d %s", code, errOut)
	}
}
