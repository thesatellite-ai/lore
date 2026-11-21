package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadServerCandidate (devco:D-833): the CLI offers the read server
// only calls that may be reads (the server still checks the operation),
// so a write never starts a server.
func TestReadServerCandidate(t *testing.T) {
	t.Parallel()
	for args, want := range map[string]bool{
		"ask --query x": true, "search foo": true, "memory list": true, "task show --id x": true,
		"sql --query x": true, "context": true, "where": true,
		"memory add x": false, "sync": false, "init": false, "--json ask": false, "": false,
		"setup run": false, "store folder add me x": false,
	} {
		if got := readServerCandidate(splitArgs(args)); got != want {
			t.Errorf("%q: candidate = %v, want %v", args, got, want)
		}
	}
}

// splitArgs splits on single spaces ("" is no args).
func splitArgs(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}

// TestReadServerSwitches (devco:D-833): LORE_READ_SERVER turns the server
// on or off, and read_server: false in config.yaml turns it off.
func TestReadServerSwitches(t *testing.T) {
	t.Parallel()
	for v, want := range map[string][2]bool{"0": {false, true}, "off": {false, true}, "false": {false, true}, "1": {true, true}, "on": {true, true}, "": {false, false}, "maybe": {false, false}} {
		on, set := readServerSwitch(v)
		if on != want[0] || set != want[1] {
			t.Errorf("LORE_READ_SERVER=%q: on %v set %v, want %v", v, on, set, want)
		}
	}
	dir := t.TempDir()
	for body, off := range map[string]bool{
		"read_server: false\n": true, "read_server: true\n": false, "upgrade_notice: false\n": false,
		"read_server: [\n": false, // broken: the command itself reports it
	} {
		p := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := configTurnsOff(p); got != off {
			t.Errorf("config %q: off = %v, want %v", body, got, off)
		}
	}
	if configTurnsOff(filepath.Join(dir, "missing.yaml")) {
		t.Error("a missing config turned the server off")
	}
}
