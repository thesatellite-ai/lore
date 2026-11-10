//go:build loretest

package host

// Migration test hooks (FORMAT_EVOLUTION 7.8), test builds only: the race
// tests crash a run at a named point or pause it there while a teammate
// acts.
//
//	LORE_TEST_MIGRATE_CRASH_AT=<point>[:<n>]  kill this process (as kill -9) the nth time
//	                                          it reaches <point> (n defaults to 1)
//	LORE_TEST_MIGRATE_PAUSE_AT=<point>        at <point>, create <dir>/<point>.paused and
//	LORE_TEST_MIGRATE_PAUSE_DIR=<dir>         wait until <dir>/<point>.resume exists
//
// Points are opsmigrate.Point values (team-locked, begun, settings, live,
// table, finish, rollback-locked, rollback-checked).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
)

// The hook environment variables (test builds only).
const (
	envMigrateCrashAt  = "LORE_TEST_MIGRATE_CRASH_AT"
	envMigratePauseAt  = "LORE_TEST_MIGRATE_PAUSE_AT"
	envMigratePauseDir = "LORE_TEST_MIGRATE_PAUSE_DIR"
)

// Pause handshake: how often the paused process looks for the resume file,
// and how long it waits before giving up (a test that never resumes fails
// with an error instead of hanging the suite).
const (
	migratePausePoll    = 10 * time.Millisecond
	migratePauseTimeout = 2 * time.Minute
)

// MigrateTestHooks installs envHooks when either variable is set (both
// binaries call it around `lore migrate run|rollback|unlock`).
func MigrateTestHooks(ctx context.Context) context.Context {
	h := envHooks{pauseAt: opsmigrate.Point(os.Getenv(envMigratePauseAt)), pauseDir: os.Getenv(envMigratePauseDir), crashN: 1, seen: map[opsmigrate.Point]int{}}
	if v := os.Getenv(envMigrateCrashAt); v != "" {
		p, n, ok := strings.Cut(v, ":")
		h.crashAt = opsmigrate.Point(p)
		if ok {
			if k, err := strconv.Atoi(n); err == nil && k > 0 {
				h.crashN = k
			}
		}
	}
	if h.crashAt == "" && h.pauseAt == "" {
		return ctx
	}
	return opsmigrate.WithHooks(ctx, &h)
}

// envHooks is one command's hooks; seen counts how often each point was
// reached (the state lives in the value the context carries, per process).
type envHooks struct {
	mu       sync.Mutex
	crashAt  opsmigrate.Point
	crashN   int
	pauseAt  opsmigrate.Point
	pauseDir string
	seen     map[opsmigrate.Point]int
}

// At implements opsmigrate.Hooks.
func (h *envHooks) At(ctx context.Context, p opsmigrate.Point) error {
	h.mu.Lock()
	h.seen[p]++
	n := h.seen[p]
	h.mu.Unlock()
	if p == h.crashAt && n == h.crashN {
		self, err := os.FindProcess(os.Getpid())
		if err == nil {
			_ = self.Kill() // SIGKILL: no deferred release runs, like a crash
		}
		select {} // the kill lands before this returns
	}
	if p != h.pauseAt || h.pauseDir == "" {
		return nil
	}
	base := filepath.Join(h.pauseDir, string(p))
	if err := os.WriteFile(base+".paused", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return err
	}
	deadline := time.Now().Add(migratePauseTimeout)
	for {
		if _, err := os.Stat(base + ".resume"); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("test hook: " + string(p) + " was never resumed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(migratePausePoll):
		}
	}
}
