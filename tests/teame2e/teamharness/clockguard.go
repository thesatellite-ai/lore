package teamharness

// Clock guard: every clock-driven team scenario (max-age copies, lease
// expiry, the fetch cache) moves an actor's clock with LORE_NOW, which only a
// `-tags loretest` build honours (cmd/lore/liveclock_loretest.go). A
// binary built without the tag silently runs on the wall clock, so those
// tests fail at an unrelated-looking assertion (TestSideEffect14's max-age
// land check raised two false alarms that way, 2026-11-05). New refuses such
// a binary up front with the fix in the message
// (World.RequireTestClock, called by the suite's newTeamWorld).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// clockProbeNow is the fixed instant the probe asks the binary to use; it is
// far from any real wall-clock time, so a match cannot be a coincidence.
var clockProbeNow = time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)

// clockProbeBody is the memory the probe writes and reads back.
const clockProbeBody = "team harness clock probe"

// clockProbes caches one probe result per absolute binary path for the life
// of the test process: the probe runs a few lore commands (about 2 s), and a
// suite builds hundreds of Worlds on the same binary. It and helpProbes
// (caps.go) are the package's only mutable package-level state; neither
// outlives the process or holds a path outside its probe's own temp dir.
var clockProbes sync.Map // abs bin path -> *clockProbe

// clockProbe is one binary's memoised probe outcome.
type clockProbe struct {
	once sync.Once
	err  error
}

// RequireTestClock fails the test at once when the World's binary ignores
// LORE_NOW (built without -tags loretest), naming the build command that
// fixes it. The probe runs once per binary path per test process.
func (w *World) RequireTestClock() {
	w.T.Helper()
	if err := checkTestClock(w.Bin); err != nil {
		w.T.Fatal(err)
	}
}

// checkTestClock returns nil when bin honours LORE_NOW, or an error that says
// how to build a binary that does. bin must be absolute.
func checkTestClock(bin string) error {
	v, _ := clockProbes.LoadOrStore(bin, &clockProbe{})
	p, ok := v.(*clockProbe)
	if !ok { // sync.Map holds only *clockProbe values (stored above)
		return fmt.Errorf("team harness: clock probe cache holds %T", v)
	}
	p.once.Do(func() { p.err = probeTestClock(bin) })
	return p.err
}

// probeTestClock runs, in a throwaway repo and HOME, `lore init`, one
// `memory add` and `memory list --json` with LORE_NOW set, and checks the
// memory's created_at is that instant.
func probeTestClock(bin string) error {
	dir, err := os.MkdirTemp("", "lore-team-clockprobe-")
	if err != nil {
		return fmt.Errorf("team harness clock probe: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	home, repo := filepath.Join(dir, "home"), filepath.Join(dir, "repo")
	for _, d := range []string{home, repo} {
		if mkErr := os.MkdirAll(d, harnessDirMode); mkErr != nil {
			return fmt.Errorf("team harness clock probe: %w", mkErr)
		}
	}
	env := probeEnv(home)
	run := func(name string, args ...string) (string, error) {
		cmd := binCmd(name, args...)
		cmd.Dir, cmd.Env, cmd.Stdin = repo, env, nil
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			return "", fmt.Errorf("team harness clock probe: %s %s: %w\n%s", filepath.Base(name), strings.Join(args, " "), runErr, out)
		}
		return string(out), nil
	}
	steps := [][]string{
		{"git", "init", "-q", "."},
		{bin, "init", "--non-interactive", "--name=clockprobe"},
		{bin, "memory", "add", clockProbeBody},
	}
	for _, s := range steps {
		if _, stepErr := run(s[0], s[1:]...); stepErr != nil {
			return stepErr
		}
	}
	out, err := run(bin, "memory", "list", "--json")
	if err != nil {
		return err
	}
	var list struct {
		Data []struct {
			Body      string    `json:"body"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return fmt.Errorf("team harness clock probe: memory list --json: %w\n%s", err, out)
	}
	for _, m := range list.Data {
		if m.Body != clockProbeBody {
			continue
		}
		if m.CreatedAt.Equal(clockProbeNow) {
			return nil
		}
		return fmt.Errorf("team harness: %s ignores %s (a row written at %s=%s got created_at %s): it was built without the loretest tag; rebuild it with `CGO_ENABLED=0 go build -tags loretest -o <bin> ./cmd/lore` (or `task lore:test:team`)",
			bin, EnvLoreNow, EnvLoreNow, clockProbeNow.Format(nowLayout), m.CreatedAt.UTC().Format(nowLayout))
	}
	return fmt.Errorf("team harness clock probe: memory list --json has no %q row\n%s", clockProbeBody, out)
}

// probeEnv is the parent's environment minus lore, git and CI state (as in
// World.baseEnv), with a fresh HOME, the probe clock and no agent detection.
func probeEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "LORE_") || strings.HasPrefix(k, "GIT_") || isCIMarker(k) || k == "HOME" {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+home,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"LORE_HUMAN=1",
		"LORE_NO_DETECT=1",
		EnvLoreNow+"="+clockProbeNow.Format(nowLayout),
	)
}
