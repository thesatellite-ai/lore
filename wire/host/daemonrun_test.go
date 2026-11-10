package host

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/op"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
)

// heartbeatCmd is a CLI command shaped like the generated `lore task
// heartbeat`: a --daemon bool flag (ops/coord HeartbeatIn.Daemon), parsed
// from args exactly as cobra parses the real command line.
func heartbeatCmd(t *testing.T, withFlag bool, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "heartbeat"}
	if withFlag {
		cmd.Flags().Bool(daemonFlag, false, "keep renewing")
	}
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return cmd
}

// TestDaemonRunCLI: the --daemon flag decides; a flag that is unset or set
// to false is the one-shot form (it runs under the live session), and a
// command that has no --daemon flag at all counts as the daemon form, the
// side that never holds the live lock for a session's lifetime.
func TestDaemonRunCLI(t *testing.T) {
	for _, c := range []struct {
		name     string
		withFlag bool
		args     []string
		want     bool
	}{
		{"flag unset", true, nil, false},
		{"--daemon", true, []string{"--daemon"}, true},
		{"--daemon=true", true, []string{"--daemon=true"}, true},
		{"--daemon=false", true, []string{"--daemon=false"}, false},
		{"other args only", true, []string{"tsk_31"}, false},
		{"no daemon flag on the command", false, nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			run := &Run{Cmd: heartbeatCmd(t, c.withFlag, c.args...)}
			if got := daemonRun(run); got != c.want {
				t.Fatalf("daemonRun(%v) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

// TestDaemonRunREST: a REST call (no cobra command) is read from its JSON
// body. An empty body is the one-shot form, as it decodes to the zero input;
// a body that cannot be read counts as the daemon form.
func TestDaemonRunREST(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want bool
	}{
		{"empty body", "", false},
		{"blank body", " \n\t", false},
		{"empty object", "{}", false},
		{"daemon true", `{"daemon":true}`, true},
		{"daemon false", `{"daemon":false}`, false},
		{"daemon with id", `{"id":"tsk_31","daemon":true}`, true},
		{"id only", `{"id":"tsk_31"}`, false},
		{"malformed", `{"daemon":`, true},
		{"daemon not a bool", `{"daemon":"yes"}`, true},
		{"not an object", `[true]`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			run := &Run{Decoder: op.JSONDecoder{Body: []byte(c.body)}}
			if got := daemonRun(run); got != c.want {
				t.Fatalf("daemonRun(body %q) = %v, want %v", c.body, got, c.want)
			}
		})
	}
	// Neither a command nor a JSON body: unreadable, so the daemon form.
	if !daemonRun(&Run{}) {
		t.Fatal("a run with no command and no JSON body must count as the daemon form")
	}
}

// TestSkipLiveRunHeartbeat: only task heartbeat --daemon skips the live
// session; a one-shot heartbeat and every other coord write (even one whose
// body says daemon) run under it, and the always-skipped nouns stay skipped.
func TestSkipLiveRunHeartbeat(t *testing.T) {
	entry := func(name string) op.Entry { return specEntry{spec: op.Spec{Name: name}} }
	for _, c := range []struct {
		name string
		run  *Run
		want bool
	}{
		{"heartbeat --daemon", &Run{Entry: entry(opscoord.OpTaskHeartbeat), Cmd: heartbeatCmd(t, true, "--daemon")}, true},
		{"heartbeat one-shot", &Run{Entry: entry(opscoord.OpTaskHeartbeat), Cmd: heartbeatCmd(t, true)}, false},
		{"heartbeat REST daemon", &Run{Entry: entry(opscoord.OpTaskHeartbeat), Decoder: op.JSONDecoder{Body: []byte(`{"daemon":true}`)}}, true},
		{"heartbeat REST one-shot", &Run{Entry: entry(opscoord.OpTaskHeartbeat), Decoder: op.JSONDecoder{Body: []byte(`{"id":"tsk_31"}`)}}, false},
		{"claim with a daemon body", &Run{Entry: entry(opscoord.OpTaskClaim), Decoder: op.JSONDecoder{Body: []byte(`{"daemon":true}`)}}, false},
		{"init", &Run{Entry: entry("init")}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := skipLiveRun(c.run); got != c.want {
				t.Fatalf("skipLiveRun = %v, want %v", got, c.want)
			}
		})
	}
}
