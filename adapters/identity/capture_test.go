package identity

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// captureDummy is the value a names-only capture line gets: the capture
// records that the variable was set, never what it held.
const captureDummy = "captured"

// capture is one live capture under testdata/captures (AGENT_IDENTITY 4,
// build step 12.2): the environment names a tool's agent shell had, the
// ancestor chain, the TTY state, and what detection must make of it.
type capture struct {
	name  string
	tty   bool
	env   map[string]string
	procs captureProcs
	want  map[string]string
	flags []string // want flag
	not   []string // want noflag
}

type captureProcs []agentdetect.Process

func (c captureProcs) Ancestors(max int) []agentdetect.Process {
	if len(c) > max {
		return c[:max]
	}
	return c
}

// readCapture parses the capture format: "tty yes|no", "proc <exe> [args]"
// nearest first, "env NAME" or "env NAME=value", "want <field> <value>",
// "want flag <f>", "want noflag <f>"; # starts a comment.
func readCapture(t *testing.T, path string) capture {
	t.Helper()
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	c := capture{name: filepath.Base(path), env: map[string]string{}, want: map[string]string{}}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kind, rest, _ := strings.Cut(line, " ")
		switch kind {
		case "tty":
			c.tty = rest == "yes"
		case "proc":
			fields := strings.Fields(rest)
			c.procs = append(c.procs, agentdetect.Process{Exe: fields[0], Args: fields[1:]})
		case "env":
			name, val, ok := strings.Cut(rest, "=")
			if !ok {
				val = captureDummy
			}
			c.env[name] = val
		case "want":
			field, val, _ := strings.Cut(rest, " ")
			switch field {
			case "flag":
				c.flags = append(c.flags, val)
			case "noflag":
				c.not = append(c.not, val)
			default:
				c.want[field] = val
			}
		default:
			t.Fatalf("%s:%d: unknown line kind %q", c.name, n, kind)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(c.want) == 0 {
		t.Fatalf("%s: a capture without a want line checks nothing", c.name)
	}
	return c
}

func (c capture) detect(tty bool, procs captureProcs) (agentdetect.Result, error) {
	return agentdetect.Detect(agentdetect.Inputs{Getenv: func(k string) string { return c.env[k] }, TTY: tty, Procs: procs})
}

// TestLiveCaptures replays every live capture through the real detector:
// the tool, its signal, the session id (present or not), the terminal and
// the flags recorded in the file must come out. A new capture is a new
// file; nothing else changes.
func TestLiveCaptures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "captures", "*.capture"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no captures found (%v)", err)
	}
	for _, path := range files {
		c := readCapture(t, path)
		t.Run(c.name, func(t *testing.T) {
			r, err := c.detect(c.tty, c.procs)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{"tool": r.Tool, "signal": r.Signal, "terminal": r.Terminal, "version": r.Version, "confidence": string(r.Confidence)}
			if r.SessionID != "" {
				got["session"] = "set"
			} else {
				got["session"] = "unset"
			}
			for field, want := range c.want {
				g, known := got[field]
				if !known {
					t.Fatalf("unknown want field %q", field)
				}
				if g != want {
					t.Errorf("%s = %q, want %q (result %+v)", field, g, want, r)
				}
			}
			for _, f := range c.flags {
				if !slices.Contains(r.Flags, f) {
					t.Errorf("flag %q missing: %v", f, r.Flags)
				}
			}
			for _, f := range c.not {
				if slices.Contains(r.Flags, f) {
					t.Errorf("flag %q set: %v", f, r.Flags)
				}
			}
		})
	}
}

// TestClaudeCaptureVariants derives the two cases a live Claude Code
// shell turns into: the person attaches to the tmux pane the agent's env
// leaked into (a TTY and no claude above the shell: the person, with
// stale_marker and one note), and the same pane with claude still above
// (a TTY does not demote a strong marker). Through Resolve, the first is
// the person and the second the person's claude_code agent.
func TestClaudeCaptureVariants(t *testing.T) {
	c := readCapture(t, filepath.Join("testdata", "captures", "claude_code_linux_tmux.capture"))
	noClaude := slices.DeleteFunc(slices.Clone(c.procs), func(p agentdetect.Process) bool { return p.Exe == "claude" })
	ctx := context.Background()
	const person = "human:tomas@agency.example"
	for _, tc := range []struct {
		name   string
		procs  captureProcs
		agent  bool
		stale  bool
		noteOK bool
	}{
		{"attached person, no claude above", noClaude, false, true, true},
		{"claude still above, with a tty", c.procs, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := Inputs{Home: t.TempDir(), Getenv: func(k string) string { return c.env[k] }, Config: Config{Identity: Entry{Key: person, Name: "tomas"}},
				Detect: func() (agentdetect.Result, error) { return c.detect(true, tc.procs) }}
			r, err := Resolve(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			if (r.Kind == KindAgent) != tc.agent || (tc.agent && r.Label != "claude_code") || (!tc.agent && r.StableKey != person) {
				t.Fatalf("kind %q key %q label %q", r.Kind, r.StableKey, r.Label)
			}
			if r.Detection == nil || slices.Contains(r.Detection.Flags, agentdetect.FlagStaleMarker) != tc.stale {
				t.Fatalf("detection %+v", r.Detection)
			}
			if hasNote := slices.ContainsFunc(r.Notes, func(n string) bool { return strings.Contains(n, "no claude_code process") }); hasNote != tc.noteOK {
				t.Fatalf("notes %v", r.Notes)
			}
		})
	}
}

// signalValue is an environment value that satisfies s (and nothing
// stronger): the capture-free half of the weak-row check.
func signalValue(s agentdetect.Signal) map[string]string {
	switch s.Match {
	case agentdetect.Equals:
		return map[string]string{s.Env: s.Value}
	case agentdetect.Prefix:
		return map[string]string{s.Env: s.Value + "-x"}
	case agentdetect.Contains:
		return map[string]string{s.Env: "x-" + s.Value + "-x"}
	case agentdetect.BothWith:
		return map[string]string{s.Env: captureDummy, s.Value: captureDummy}
	default:
		return map[string]string{s.Env: captureDummy}
	}
}

// TestWeakRowsMatrix covers every weak signal of every built-in row that
// has no live capture yet (OQ11, section 4 "What ships in 0.1.18"): alone
// with a TTY it is a person, alone without a TTY it is that tool with
// weak_only and the row's confidence, and with the person's identity
// pinned by LORE_HUMAN=1 it stays the person. A row promoted to strong
// after a capture drops out of this matrix by itself.
func TestWeakRowsMatrix(t *testing.T) {
	ctx := context.Background()
	cases := 0
	for _, d := range agentdetect.Builtin() {
		for _, s := range d.Weak {
			env := signalValue(s)
			name := d.Tool + "/" + s.Env
			t.Run(name, func(t *testing.T) {
				get := func(k string) string { return env[k] }
				r, err := agentdetect.Detect(agentdetect.Inputs{Getenv: get, TTY: true})
				if err != nil || r.IsAgent() {
					t.Fatalf("with a tty: %+v %v", r, err)
				}
				r, err = agentdetect.Detect(agentdetect.Inputs{Getenv: get, TTY: false})
				if err != nil || r.Tool != d.Tool || r.Signal != s.Env || r.Confidence != d.Confidence || !slices.Contains(r.Flags, agentdetect.FlagWeakOnly) {
					t.Fatalf("without a tty: %+v %v", r, err)
				}
				withHuman := map[string]string{EnvHuman: "1"}
				for k, v := range env {
					withHuman[k] = v
				}
				in := Inputs{Home: t.TempDir(), Getenv: func(k string) string { return withHuman[k] }, Config: Config{Identity: Entry{Key: "human:tomas@agency.example"}},
					Detect: func() (agentdetect.Result, error) {
						return agentdetect.Detect(agentdetect.Inputs{Getenv: func(k string) string { return withHuman[k] }})
					}}
				p, err := Resolve(ctx, in)
				if err != nil || p.Kind != KindHuman || !p.DeclaredHuman || !slices.Contains(p.Detection.Flags, FlagHumanOverAgent) {
					t.Fatalf("LORE_HUMAN=1: %+v %v", p, err)
				}
			})
			cases++
		}
	}
	// The design names at least these weak rows (section 4 table); the
	// matrix must not silently shrink to nothing.
	if cases < 10 {
		t.Fatalf("only %d weak signals in the table", cases)
	}
}
