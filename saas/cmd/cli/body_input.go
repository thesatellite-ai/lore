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

	"github.com/spf13/cobra"

	"saas/pkg/aicoder/errcodes"
)

// bodyOnlyArgs is the cobra Args validator of every body-primary `add`
// command: it refuses positional text with the same message as
// resolveBodyInput. Declaring it (rather than only failing inside RunE)
// makes the rule visible to cobra, so the help, the error text and the docs
// drift test (TestDocsMatchCLI, which runs ValidateArgs on every documented
// command) all agree.
func bodyOnlyArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return positionalBodyError()
	}
	return nil
}

// positionalBodyError is the usage error for text passed without --body.
func positionalBodyError() error {
	return errcodes.New(errcodes.InvalidInput,
		"body must be passed via --body=<value> (or piped via stdin)").
		WithHint("example: lore <cmd> add --title=X --body=\"the body\"")
}

// resolveBodyInput is the canonical entry point for body-primary `add`
// commands. Pass the value of the --body flag; positional args (if any)
// are treated as a usage error
//
// Pass nil/empty args to indicate "no positional, only flag + stdin"
func resolveBodyInput(args []string, bodyFlag string) (string, error) {
	if len(args) > 0 {
		// Caller had positional args left over — usage error (bodyOnlyArgs
		// normally refuses them before RunE runs).
		return "", positionalBodyError()
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
