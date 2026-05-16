// Package style provides semantic terminal styling for lore CLI output.
//
// Three modes (R15 #19):
//
//	auto    — emit ANSI codes only when stdout is a TTY (default)
//	always  — emit ANSI regardless (for piped output that you want colored)
//	never   — never emit ANSI (for plain text capture / CI logs)
//
// Honors the standard env vars:
//
//	NO_COLOR=<anything>       force never  (https://no-color.org)
//	FORCE_COLOR=<anything>    force always (https://force-color.org)
//
// Precedence (highest first):
//
//  1. Mode set via SetMode (e.g. from --color flag)
//  2. NO_COLOR env var
//  3. FORCE_COLOR env var
//  4. TTY auto-detect on os.Stdout
//
// Usage:
//
//	style.Init()  // call once at main() startup
//	fmt.Println(style.Success("✓ done"))
//	fmt.Println(style.Error("✗ failed"))
//
// Tokens are semantic, not raw colors — keep call sites readable and let the
// theme change centrally.
package style

import (
	"fmt"
	"os"
	"sync/atomic"
)

// Mode controls when ANSI escape codes are emitted.
type Mode int

const (
	ModeAuto   Mode = iota // TTY-detect (default)
	ModeAlways             // always emit codes
	ModeNever              // never emit codes
)

// String returns the canonical name for a Mode (matches --color flag values).
func (m Mode) String() string {
	switch m {
	case ModeAlways:
		return "always"
	case ModeNever:
		return "never"
	default:
		return "auto"
	}
}

// ParseMode parses a flag value or env hint into a Mode. Returns ModeAuto for
// unknown values (silent, since downstream Init() will resolve).
func ParseMode(s string) Mode {
	switch s {
	case "always", "yes", "on", "true", "1":
		return ModeAlways
	case "never", "no", "off", "false", "0":
		return ModeNever
	default:
		return ModeAuto
	}
}

// enabled is the resolved boolean; atomic so we can check from any goroutine.
// Set by Init() based on Mode + env + TTY.
var enabled atomic.Bool

// resolvedMode is the mode that was set (mostly for diagnostic / doctor output).
var resolvedMode atomic.Int32

// Init resolves whether ANSI codes will be emitted.
//
// The flagMode argument is the value from the --color flag (or ModeAuto if
// the flag was not provided). Init applies the precedence chain documented
// in the package doc.
func Init(flagMode Mode) {
	mode := flagMode

	// If the flag was explicit, it wins. Otherwise consult env.
	// Per https://no-color.org and https://force-color.org, the env vars are
	// honored only when SET AND NON-EMPTY (an empty value is treated as unset).
	if mode == ModeAuto {
		if os.Getenv("NO_COLOR") != "" {
			mode = ModeNever
		} else if os.Getenv("FORCE_COLOR") != "" {
			mode = ModeAlways
		}
	}

	switch mode {
	case ModeAlways:
		enabled.Store(true)
	case ModeNever:
		enabled.Store(false)
	default: // ModeAuto
		enabled.Store(isTerminal(os.Stdout))
	}
	resolvedMode.Store(int32(mode))
}

// Enabled returns the current resolved state.
func Enabled() bool {
	return enabled.Load()
}

// CurrentMode returns the resolved mode for diagnostic output.
func CurrentMode() Mode {
	return Mode(resolvedMode.Load())
}

// ANSI escape codes. Plain stdlib — keep this list tiny.
const (
	reset       = "\x1b[0m"
	bold        = "\x1b[1m"
	dim         = "\x1b[2m"
	red         = "\x1b[31m"
	green       = "\x1b[32m"
	yellow      = "\x1b[33m"
	blue        = "\x1b[34m"
	magenta     = "\x1b[35m"
	cyan        = "\x1b[36m"
	brightRed   = "\x1b[91m"
	brightGreen = "\x1b[92m"
)

// wrap conditionally wraps s with ANSI escape codes.
func wrap(s, code string) string {
	if !enabled.Load() {
		return s
	}
	return code + s + reset
}

// Semantic style tokens. Add new tokens here when introducing new UX states.

// Success styles a "operation completed" string.
func Success(s string) string { return wrap(s, brightGreen) }

// Error styles an error message header.
func Error(s string) string { return wrap(s, brightRed+bold) }

// Warn styles a warning header.
func Warn(s string) string { return wrap(s, yellow) }

// Info styles an informational annotation.
func Info(s string) string { return wrap(s, cyan) }

// Hint styles a "try this" actionable hint.
func Hint(s string) string { return wrap(s, blue) }

// Muted styles secondary / less-important text.
func Muted(s string) string { return wrap(s, dim) }

// Code styles inline code references (file paths, identifiers).
func Code(s string) string { return wrap(s, magenta) }

// Bold emphasizes plain text without color.
func Bold(s string) string { return wrap(s, bold) }

// ScopeBadge formats a search-result scope tag like [repo:web1] / [master] / [global].
func ScopeBadge(scope string) string {
	return wrap(fmt.Sprintf("[%s]", scope), cyan)
}
