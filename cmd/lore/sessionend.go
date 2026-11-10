package main

// sessionend.go — `lore agent end --tool <slug>`, the coding tool's
// session-end hook (decided 2026-11-10, land7 A; installed by `lore setup
// --agent-hooks` beside the session-start hook, logic/toolprofile
// SessionEndHooks). It pushes the store batches still waiting when the
// session closes (store push modes: lore runs no daemon or timer, so the
// end of a session is one of the moments a batch can go out).
//
//	main ─▶ isSessionEndHook? ─▶ runSessionEnd
//	          stdout/stderr → /dev/null   (never a line in the tool)
//	          deadline wire.SessionEndBudget (the 15 s piggyback limit)
//	          watchdog: exit 0 at budget + sessionEndGrace, whatever runs
//	          recover() a panic
//	          run(args) ─▶ the hidden `agent end` command ─▶ App.FlushBatches
//	        ─▶ exit 0, always
//
// A hook must never block or fail the tool's session: every outcome is
// exit 0 with no output; a store that did not go out keeps its commits for
// the next command or `lore sync`.

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/wire"
)

// Words and flag of the session-end hook (toolprofile.EndHookCommand).
const (
	sessionEndNoun = "agent"
	sessionEndVerb = "end"
	sessionEndTool = "tool"
)

// sessionEndGrace is how long past the budget the watchdog lets a push
// that is already finishing run before the process exits anyway.
const sessionEndGrace = 3 * time.Second

// isSessionEndHook reports whether args run the session-end hook: the
// first two words that are not flags are `agent end`.
func isSessionEndHook(args []string) bool {
	var words []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if words = append(words, a); len(words) == 2 {
			break
		}
	}
	return len(words) == 2 && words[0] == sessionEndNoun && words[1] == sessionEndVerb
}

// runSessionEnd runs the hook under its guarantees and returns 0.
func runSessionEnd(args []string) (code int) {
	if null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = null, null // nothing reaches the tool's screen
	}
	defer func() {
		_ = recover() // a crash in a hook is still a quiet exit 0
		code = 0
	}()
	watchdog := time.AfterFunc(wire.SessionEndBudget+sessionEndGrace, func() { os.Exit(0) })
	defer watchdog.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), wire.SessionEndBudget)
	defer cancel()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	_ = run(ctx, args)
	return 0
}

// mountSessionEnd adds `lore agent end` under the agent noun.
func mountSessionEnd(root *cobra.Command, app *wire.App) {
	if agent := find(root, sessionEndNoun); agent != nil {
		agent.AddCommand(sessionEndCommand(app))
	}
}

// sessionEndCommand is the hidden `lore agent end --tool <slug>`: push the
// waiting batches within the context's deadline. Hidden like `lore sync
// hook`: it is an entry point for the tools' hook files, not a command a
// person types.
func sessionEndCommand(app *wire.App) *cobra.Command {
	c := &cobra.Command{
		Use:    sessionEndVerb,
		Short:  "Entry point for the coding tools' session-end hook: push the store batches still waiting",
		Hidden: true,
		Args:   cobra.ArbitraryArgs, // a tool may pass more; the hook never fails on it
		RunE: func(cmd *cobra.Command, _ []string) error {
			app.FlushBatches(cmd.Context())
			return nil
		},
	}
	c.Flags().String(sessionEndTool, "", "the tool whose session ended (claude_code, codex, gemini_cli)")
	c.FParseErrWhitelist.UnknownFlags = true // a newer hook line never fails an older lore
	return c
}
