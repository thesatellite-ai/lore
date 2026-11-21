package app

// readserver.go — the optional local read server (devco:D-833): what every
// platform shares (the switches, the request, the client's word filter).
// The server and the client are readserver_unix.go (linux, darwin); other
// platforms run every command one-shot (readserver_other.go).
//
// Why: a one-shot `lore ask` cannot answer in under 50 ms: starting the
// process and parsing the schemas of every folder cache once is already
// ~55 ms. The server is one long-lived lore process per lore home that
// runs read commands for the CLI with its caches, op registry and lenses
// already warm:
//
//	lore ask … ─▶ Main ─▶ readServerWanted? ─no──────────────▶ Run (one-shot)
//	                  │ yes
//	                  ├─ dial <lore home>/run/read.sock ─fail─▶ start the server
//	                  │                                        detached, Run one-shot
//	                  ├─ send args, cwd, env, parent pid + stdin/stdout/stderr
//	                  │  (SCM_RIGHTS); the server checks uid, binary stamp and
//	                  │  that the call is one read-only operation (cli.ReadCall)
//	                  ├─ declined, busy, or no answer ─────────▶ Run (one-shot)
//	                  └─ served: exit with the server's exit code
//
// The server runs the same run() the one-shot process runs, with the
// client's working directory, environment, arguments, parent pid (agent
// detection) and its three stdio file descriptors in place of its own, so
// the output is the same byte for byte; it serves one command at a time
// and exits after readServerIdle without a call, when its socket is
// removed, or when the lore binary changes.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/lorehome"
)

const (
	// envReadServer turns the read server off ("0", "off", "false") or on
	// ("1", "on", "true") for one command; unset follows the config key
	// and the build's default.
	envReadServer = "LORE_READ_SERVER"
	// configReadServer is the config.yaml key (a bool) that turns the read
	// server off for the person (read_server: false).
	configReadServer = "read_server"
	// readServerWord is the hidden first argument the CLI starts the
	// server with; Main runs the server for it, never cobra.
	readServerWord = "serve-reads"
	// readServerIdle is how long the server waits for a call before it
	// exits (owner decision: 10 minutes).
	readServerIdle = 10 * time.Minute
	// readServerMaxLife bounds one server's life however busy it is: the
	// next read after it starts a fresh one, so nothing a process keeps
	// between commands (git answers by object id, lenses) grows past it.
	readServerMaxLife = time.Hour
	// readServerDir and readServerSock are where the socket lives in the
	// lore home: <home>/run/read.sock (the folder 0700, the socket 0600).
	readServerDir  = "run"
	readServerSock = "read.sock"
	// readServerLock is held by the running server (flock) so a second one
	// started at the same moment exits; readServerStart marks a start the
	// CLI asked for, so calls in the next readServerRestart start no more.
	readServerLock    = "read.lock"
	readServerStart   = "read.start"
	readServerRestart = 30 * time.Second
	// readServerProto is the request format; a server answers only its own.
	readServerProto = 1
)

// readRequest is what the CLI sends the server for one command.
type readRequest struct {
	V      int      `json:"v"`
	Stamp  string   `json:"stamp"`  // the client binary (binaryStamp): a different lore never answers
	Args   []string `json:"args"`   // the command's arguments (os.Args[1:])
	Dir    string   `json:"dir"`    // the client's working directory
	Env    []string `json:"env"`    // the client's whole environment
	Parent int      `json:"parent"` // the client's parent pid: agent detection walks from it
	Umask  int      `json:"umask"`  // the client's umask: files the command makes get the same mode
}

// readReply is the server's answer: first Accepted (or Served false with
// Why), then, after the CLI says go, Served with the exit code.
type readReply struct {
	Accepted bool   `json:"accepted,omitempty"`
	Served   bool   `json:"served,omitempty"`
	Code     int    `json:"code"`
	Why      string `json:"why,omitempty"`
	// Pipe: a write to the client's stdout or stderr hit a closed pipe
	// (`lore ask | head -1`); the CLI then ends by SIGPIPE as a one-shot
	// lore would.
	Pipe bool `json:"pipe,omitempty"`
}

// readServerCandidates are the first words of the commands the CLI offers
// to the server, and readServerVerbs the later words that make a call a
// candidate: the server still serves only an operation that only reads
// (cli.ReadCall). The filter keeps a write from starting a server.
var (
	readServerCandidates = []string{"ask", "search", "context", "sql", "where", "whoami"}
	readServerVerbs      = []string{"list", "show", "search"}
)

// readServerCandidate reports whether args may be a read the server runs.
func readServerCandidate(args []string) bool {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return false
	}
	if slices.Contains(readServerCandidates, args[0]) {
		return true
	}
	for _, a := range args[1:min(len(args), 3)] {
		if slices.Contains(readServerVerbs, a) {
			return true
		}
	}
	return false
}

// readServerSwitch is what envReadServer says: on, off, or unset (follow
// the config and the default).
func readServerSwitch(v string) (on, set bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "off", "false", "no":
		return false, true
	case "1", "on", "true", "yes":
		return true, true
	}
	return false, false
}

// readServerWanted reports whether the CLI should ask the server to run
// args, and the lore home it serves: on when LORE_READ_SERVER says so,
// else off in CI (one-shot runners leave no process behind), off when
// config.yaml says read_server: false, else the build's default (on in a
// release build, off in a test build so suites start no server unless a
// test turns it on).
func readServerWanted(args []string) (string, bool) {
	if !readServerCandidate(args) {
		return "", false
	}
	home, err := lorehome.Resolve()
	if err != nil {
		return "", false
	}
	if on, set := readServerSwitch(os.Getenv(envReadServer)); set {
		return home.Dir, on
	}
	if os.Getenv("CI") != "" || !readServerDefault {
		return "", false
	}
	return home.Dir, !configTurnsOff(filepath.Join(home.Dir, "config.yaml"))
}

// configTurnsOff reports whether the config file at path sets read_server
// to false. A missing or unreadable file leaves the default; a broken one
// too (the command itself reports a broken config, one-shot or served).
func configTurnsOff(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), configReadServer) {
		return false
	}
	var c struct {
		ReadServer *bool `yaml:"read_server"`
	}
	if yaml.Unmarshal(b, &c) != nil {
		return false
	}
	return c.ReadServer != nil && !*c.ReadServer
}

// readServerPaths are the run folder and the socket of the lore home.
func readServerPaths(home string) (dir, sock string) {
	dir = filepath.Join(home, readServerDir)
	return dir, filepath.Join(dir, readServerSock)
}
