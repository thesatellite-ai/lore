// body_input.go — shared helper for `<kind> add` body-input handling
//
// ONE canonical shape across every `add` command:
//
//  1. `--body=<v>` flag       → canonical
//  2. stdin (when no TTY)     → fallback if --body empty (lets you pipe)
//  3. positional args          → ERROR — point at --body
//  4. neither flag nor stdin  → ERROR — body required
//
// Per user direction: `--body=` is the only way to pass body. Positional
// args on body-primary `add` commands are a usage error
package main

import (
	"os"
	"strings"

	"saas/pkg/aicoder/errcodes"
)

// resolveBodyInput is the canonical entry point for body-primary `add`
// commands. Pass the value of the --body flag; positional args (if any)
// are treated as a usage error
//
// Pass nil/empty args to indicate "no positional, only flag + stdin"
func resolveBodyInput(args []string, bodyFlag string) (string, error) {
	if len(args) > 0 {
		// Caller had positional args left over — usage error
		return "", errcodes.New(errcodes.InvalidInput,
			"body must be passed via --body=<value> (or piped via stdin)").
			WithHint("example: lore <cmd> add --title=X --body=\"the body\"")
	}
	if bodyFlag != "" {
		return bodyFlag, nil
	}
	// Stdin fallback: lets you do `cat file.md | lore memory add`
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		s := strings.TrimSpace(sb.String())
		if s != "" {
			return s, nil
		}
	}
	return "", errcodes.New(errcodes.InvalidInput,
		"body required").
		WithHint("pass --body=\"<text>\" or pipe via stdin")
}
