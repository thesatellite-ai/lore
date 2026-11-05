package cli

// colour.go — the global `--color auto|always|never` flag (CLI_REFERENCE
// 1.1, v0.1.17's style package): which lines the surface colours and how
// the binary decides whether a stream gets colour.
//
//	--color ──▶ ColorFlag (refuses any other value) ──▶ binary resolves a
//	Palette per Stream (ColorOn: always / never / auto = a terminal with
//	NO_COLOR unset) ──▶ WithColor ──▶ printHuman: "✓" marks green, "!"
//	marks and warning lines yellow; printError: "ERROR:" red, hint blue
//
// Nothing here reads the environment or a terminal: the binary injects
// both, so tests drive every mode without a TTY. A Palette that is off
// returns its input unchanged, so `never` (and every pipe under `auto`)
// prints exactly the plain bytes.

import (
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// ColorMode is a value of the global --color flag.
type ColorMode string

// The three --color values (CLI_REFERENCE 1.1); the closed set.
const (
	ColorAuto   ColorMode = "auto"
	ColorAlways ColorMode = "always"
	ColorNever  ColorMode = "never"
)

// FlagColor is the global flag's name.
const FlagColor = "color"

// colorModes lists the accepted values, in help order.
var colorModes = []ColorMode{ColorAuto, ColorAlways, ColorNever}

// ParseColorMode reads a --color value; anything outside the closed set is
// E_INVALID_INPUT (never silently treated as auto: a typo would otherwise
// look like "colour is broken").
func ParseColorMode(s string) (ColorMode, error) {
	for _, m := range colorModes {
		if string(m) == s {
			return m, nil
		}
	}
	return "", errs.New(errs.InvalidInput, "--color must be auto, always or never, not "+quoteValue(s))
}

// quoteValue quotes a flag value for an error message.
func quoteValue(s string) string { return "\"" + s + "\"" }

// ColorFlag is the --color flag's pflag.Value: it holds the parsed mode and
// refuses an unknown value at parse time. The zero value is auto.
type ColorFlag struct{ mode ColorMode }

// String implements pflag.Value.
func (f *ColorFlag) String() string { return string(f.Mode()) }

// Set implements pflag.Value.
func (f *ColorFlag) Set(s string) error {
	m, err := ParseColorMode(s)
	if err != nil {
		return err
	}
	f.mode = m
	return nil
}

// Type implements pflag.Value (the placeholder in --help).
func (*ColorFlag) Type() string { return "auto|always|never" }

// Mode is the parsed mode (auto when the flag was not given).
func (f *ColorFlag) Mode() ColorMode {
	if f == nil || f.mode == "" {
		return ColorAuto
	}
	return f.mode
}

// ColorOn decides whether one output stream gets colour: always and never
// are absolute; auto colours only a terminal with NO_COLOR unset or empty
// (https://no-color.org: a set, non-empty value turns colour off).
func ColorOn(mode ColorMode, terminal bool, noColor string) bool {
	switch mode {
	case ColorAlways:
		return true
	case ColorNever:
		return false
	case ColorAuto:
		return terminal && noColor == ""
	}
	return false // unreachable for a parsed mode; fail closed to plain text
}

// ANSI escape sequences of the palette (v0.1.17's style package values).
const (
	ansiReset       = "\x1b[0m"
	ansiBold        = "\x1b[1m"
	ansiDim         = "\x1b[2m"
	ansiYellow      = "\x1b[33m"
	ansiBlue        = "\x1b[34m"
	ansiBrightRed   = "\x1b[91m"
	ansiBrightGreen = "\x1b[92m"
)

// Palette colours the surface's human lines; the zero Palette is off and
// returns every string unchanged.
type Palette struct{ On bool }

func (p Palette) wrap(s, code string) string {
	if !p.On || s == "" {
		return s
	}
	return code + s + ansiReset
}

// Success colours a success mark ("✓").
func (p Palette) Success(s string) string { return p.wrap(s, ansiBrightGreen) }

// Warn colours a warning line or mark.
func (p Palette) Warn(s string) string { return p.wrap(s, ansiYellow) }

// Error colours the "ERROR:" prefix of an error line.
func (p Palette) Error(s string) string { return p.wrap(s, ansiBrightRed+ansiBold) }

// Hint colours the text of a hint line.
func (p Palette) Hint(s string) string { return p.wrap(s, ansiBlue) }

// Muted colours secondary text.
func (p Palette) Muted(s string) string { return p.wrap(s, ansiDim) }

// Line marks the surface recognises in human output (the shapes the
// operations' Texter forms print, CLI_REFERENCE 1.4 and v0.1.17).
const (
	successMark   = "✓"
	warnMark      = "!"
	warnLineUpper = "WARN:"
	warnLineLower = "warning:"
)

// Lines colours each line of human output: a leading "✓" mark as success,
// a leading "!" mark as a warning, and a whole "WARN:" / "warning:" line
// (after its indent) as a warning. Any other line, and every line when the
// palette is off, is left as is.
func (p Palette) Lines(text string) string {
	if !p.On {
		return text
	}
	parts := strings.SplitAfter(text, "\n")
	for i, line := range parts {
		body, nl := strings.CutSuffix(line, "\n")
		rest := strings.TrimLeft(body, " \t")
		indent := body[:len(body)-len(rest)]
		var nb string
		switch {
		case strings.HasPrefix(rest, successMark):
			nb = indent + p.Success(successMark) + strings.TrimPrefix(rest, successMark)
		case strings.HasPrefix(rest, warnMark+" "):
			nb = indent + p.Warn(warnMark) + strings.TrimPrefix(rest, warnMark)
		case strings.HasPrefix(rest, warnLineUpper), strings.HasPrefix(rest, warnLineLower):
			nb = indent + p.Warn(rest)
		default:
			continue
		}
		if nl {
			nb += "\n"
		}
		parts[i] = nb
	}
	return strings.Join(parts, "")
}

// Stream names an output stream of the process. The surface asks by
// stream, not by writer: a host hook may buffer stdout (wire/host's compat
// hook does), so the writer a command sees is not the terminal.
type Stream int

// The two streams the surface prints to.
const (
	StreamOut Stream = iota // results
	StreamErr               // errors, hints, prompts
)

// WithColor injects the palette of each output stream (the binary resolves
// --color and the terminal check of that stream). Without it the surface
// prints plain text.
func WithColor(f func(Stream) Palette) Option {
	return func(c *config) { c.color = f }
}
