package cli

import (
	"io"
	"strconv"
	"strings"
)

// PrintError writes an error no generated command printed (a host
// refusal before the command tree exists: E_ROOT_REFUSED, a failed app
// build, an error cobra returned unprinted) exactly as a generated
// command prints its own: with asJSON the one-document error envelope on
// stdout (CLI_REFERENCE 1.4), else the plain "ERROR: [code] message" line
// and hint on stderr; every byte masked like all surface output. It
// returns the code's class exit. Colour is off: the host prints before
// --color is parsed.
func PrintError(stdout, stderr io.Writer, err error, asJSON bool) int {
	return printError(stdout, stderr, err, asJSON, Palette{})
}

// JSONRequested reports whether args (the process arguments without the
// program name) ask for --json, for an error printed before cobra parsed
// them: "--json", or "--json=<bool>" with a true value. Arguments after
// "--" are operands, never flags. A malformed --json=<value> counts as
// not asked, as cobra would refuse it anyway.
func JSONRequested(args []string) bool {
	flag := "--" + FlagJSON
	asked := false
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == flag {
			asked = true
			continue
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			b, err := strconv.ParseBool(v)
			asked = err == nil && b
		}
	}
	return asked
}
