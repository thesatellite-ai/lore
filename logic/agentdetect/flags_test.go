package agentdetect

import (
	"slices"
	"strings"
	"testing"
)

// TestDetectStaleMarker pins 10.4 as a matrix over signal kind x TTY x walk:
// only a strong env marker, with a terminal on stdin and a walk that ran
// without finding the tool (for a tool the walk can recognise), resolves the
// person with stale_marker and a note. Weak, walk, plugin and convention
// signals, and tools without Procs, are never stale.
func TestDetectStaleMarker(t *testing.T) {
	shellOnly := fakeProcs{{Exe: "/bin/zsh"}, {Exe: "tmux"}}
	withClaude := fakeProcs{{Exe: "/bin/zsh"}, {Exe: "/usr/local/bin/claude"}}
	inhouse := []Detector{{Tool: "inhouse", Strong: []Signal{eq("INHOUSE_AGENT", "1")}, Procs: []ProcMatch{{Exe: "inhouse"}}}}
	cases := []struct {
		name  string
		env   map[string]string
		tty   bool
		procs fakeProcs
		extra []Detector
		tool  string // "" = person
		stale bool
	}{
		{"strong+tty+walk without tool", map[string]string{"CLAUDE_CODE_CHILD_SESSION": "1"}, true, shellOnly, nil, "", true},
		{"strong+tty+walk with tool", map[string]string{"CLAUDE_CODE_CHILD_SESSION": "1"}, true, withClaude, nil, "claude_code", false},
		{"strong+tty+no walk trusts env", map[string]string{"CLAUDE_CODE_CHILD_SESSION": "1"}, true, nil, nil, "claude_code", false},
		{"strong+no tty+walk without tool", map[string]string{"CLAUDE_CODE_CHILD_SESSION": "1"}, false, shellOnly, nil, "claude_code", false},
		{"strong codex+tty+walk without tool", map[string]string{"CODEX_THREAD_ID": "t"}, true, shellOnly, nil, "", true},
		{"weak+no tty+walk without tool", map[string]string{"CLAUDECODE": "1"}, false, shellOnly, nil, "claude_code", false},
		{"walk-only hit", map[string]string{}, true, withClaude, nil, "claude_code", false},
		{"plugin strong+tty+walk without tool", map[string]string{"INHOUSE_AGENT": "1"}, true, shellOnly, inhouse, "inhouse", false},
		{"convention named tool without Procs", map[string]string{"AI_AGENT": "github_copilot_vscode_agent"}, true, shellOnly, nil, "copilot_vscode", false},
		{"convention unknown agent", map[string]string{"AI_AGENT": "1"}, true, shellOnly, nil, UnknownAgent, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var procs ProcessSource
			if c.procs != nil {
				procs = c.procs
			}
			r, err := Detect(Inputs{Getenv: envOf(c.env), TTY: c.tty, Procs: procs, Extra: c.extra})
			if err != nil {
				t.Fatal(err)
			}
			if r.Tool != c.tool {
				t.Fatalf("tool %q, want %q (%+v)", r.Tool, c.tool, r)
			}
			if got := slices.Contains(r.Flags, FlagStaleMarker); got != c.stale {
				t.Fatalf("stale_marker %v, want %v (%+v)", got, c.stale, r)
			}
			if c.stale {
				if r.Note == "" || !strings.Contains(r.Note, "treating you as a person") {
					t.Fatalf("stale note %q", r.Note)
				}
			} else if r.Note != "" {
				t.Fatalf("unexpected note %q", r.Note)
			}
		})
	}
}

// TestDetectMarkersRemoved pins 10.6: a walk-only hit of a tool that sets
// strong markers carries markers_removed; a walk-only hit of a tool with no
// markers (aider) or a hit by env never does.
func TestDetectMarkersRemoved(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		procs   fakeProcs
		tool    string
		removed bool
	}{
		{"claude walk only", nil, fakeProcs{{Exe: "bash"}, {Exe: "claude"}}, "claude_code", true},
		{"codex walk only", nil, fakeProcs{{Exe: "env"}, {Exe: "codex"}}, "codex", true},
		{"aider walk only (no markers exist)", nil, fakeProcs{{Exe: "python3", Args: []string{"/x/aider"}}}, "aider", false},
		{"claude env + walk", map[string]string{"CLAUDE_CODE_CHILD_SESSION": "1"}, fakeProcs{{Exe: "claude"}}, "claude_code", false},
		{"amp weak-only tool walk only", nil, fakeProcs{{Exe: "amp"}}, "amp", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := Detect(Inputs{Getenv: envOf(c.env), TTY: true, Procs: c.procs})
			if err != nil {
				t.Fatal(err)
			}
			if r.Tool != c.tool || slices.Contains(r.Flags, FlagMarkersRemoved) != c.removed {
				t.Fatalf("got %q flags %v, want %q removed=%v", r.Tool, r.Flags, c.tool, c.removed)
			}
		})
	}
}

// TestDetectSSH pins 10.2: the ssh flag needs both SSH_CONNECTION and an
// sshd ancestor; the address value never reaches the result; the allowlist
// test still holds with SSH_CONNECTION in it.
func TestDetectSSH(t *testing.T) {
	const addr = "203.0.113.7 51234 198.51.100.2 22"
	sshd := fakeProcs{{Exe: "/bin/bash"}, {Exe: "/usr/sbin/sshd"}}
	cases := []struct {
		name  string
		env   map[string]string
		procs fakeProcs
		ssh   bool
	}{
		{"env + sshd", map[string]string{EnvSSHConnection: addr}, sshd, true},
		{"env + sshd-session (OpenSSH 9.8)", map[string]string{EnvSSHConnection: addr}, fakeProcs{{Exe: "sshd-session"}}, true},
		{"env, no sshd", map[string]string{EnvSSHConnection: addr}, fakeProcs{{Exe: "bash"}, {Exe: "tmux"}}, false},
		{"env, no walk", map[string]string{EnvSSHConnection: addr}, nil, false},
		{"sshd, no env", nil, sshd, false},
		{"agent over ssh", map[string]string{EnvSSHConnection: addr, "CODEX_THREAD_ID": "t"}, fakeProcs{{Exe: "codex"}, {Exe: "sshd"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var procs ProcessSource
			if c.procs != nil {
				procs = c.procs
			}
			r, err := Detect(Inputs{Getenv: envOf(c.env), TTY: true, Procs: procs})
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(r.Flags, FlagSSH) != c.ssh {
				t.Fatalf("ssh flag %v, want %v (%v)", !c.ssh, c.ssh, r.Flags)
			}
			blob := strings.Join(append(r.Flags, r.Tool, r.Signal, r.SessionID, r.Terminal, r.Note), "|")
			for _, v := range r.Sources {
				blob += "|" + v
			}
			if strings.Contains(blob, "203.0.113.7") {
				t.Fatal("the SSH_CONNECTION value reached the result")
			}
		})
	}
	if !slices.Contains(AllowedNames(nil), EnvSSHConnection) {
		t.Fatal("SSH_CONNECTION is not on the allowlist")
	}
	for _, n := range AllowedNames(nil) {
		if tokenLikeRe.MatchString(n) {
			t.Errorf("allowlisted name %q looks token-like", n)
		}
	}
}
