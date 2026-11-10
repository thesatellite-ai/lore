package metalist

import (
	"strings"

	"github.com/thesatellite-ai/lore/engine"
)

// warnPrefix starts the human line of a write warning that is not a
// metadata note; the CLI palette colours a "warning:" line (surface/cli
// colour.go warnLineLower).
const warnPrefix = "warning: "

// warnCodePrefix starts every engine warning code (engine.WarnCode).
const warnCodePrefix = "W_"

// NoteLine is the human line of one write warning; every noun's add/edit
// Text prints these after its own line (OV10), so a person sees what
// --json reports (T-TESTS-2: a warning the op returns reaches a person):
//
//	W_META (a new key, a "stored as" line, a default, the size warning)
//	    → the message as is, the way `lore meta set` prints it
//	any other W_ code (W_AUDIT_DEFERRED, W_SCAN_DEGRADED, …)
//	    → "warning: <message> (<code>)", one line (newlines folded)
//	an empty W_META message, or a code that is not a W_ code → ""
func NoteLine(code, msg string) string {
	if code == string(engine.WarnMeta) {
		if msg == "" {
			return ""
		}
		return msg + "\n"
	}
	if !strings.HasPrefix(code, warnCodePrefix) {
		return ""
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if msg == "" {
		return warnPrefix + code + "\n"
	}
	return warnPrefix + msg + " (" + code + ")\n"
}
