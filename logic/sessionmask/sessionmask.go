// Package sessionmask masks tool session ids in command output (layer L1,
// stdlib and logic/agentdetect only).
//
// Why (PASS3-A-7, owner, supersedes FB-SESS-2): session ids are
// internal-only (AGENT_IDENTITY 9, R11/OQ14). Masking only the ids read
// from lore.db failed open: while lore.db was unreadable the list was
// empty and a session id in output printed in clear. The mask therefore
// never depends on lore.db: it always masks
//
//	shape   every UUID-shaped token (8-4-4-4-12 hex, either case): the
//	        shape of every built-in tool's session id (Claude Code,
//	        Codex, opencode's tail, Amp's T-<uuid> tail, ...)
//	env     the value of every session variable a built-in detector
//	        reads (agentdetect SessionEnv, plus LORE_SESSION), whatever
//	        its shape: the current session is known without lore.db
//	known   any extra ids the caller passes (lore.db's list when it can
//	        be read): an addition, never a precondition
//
// Lore's own ids (prefix_32hex, no dashes) are not UUID-shaped, so row ids
// stay readable. A token shorter than MinIDLen is never masked by value:
// it would match ordinary text (the same floor as the write-time rule).
package sessionmask

import (
	"regexp"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// Redacted replaces a masked session id; the wording follows the whoami
// redaction ("session <redacted>", AGENT_IDENTITY 9).
const Redacted = "<redacted>"

// MinIDLen is the shortest id masked by value (known or env ids).
const MinIDLen = 8

// EnvLoreSession is lore's own agent session variable (`lore agent
// start`, saas identity.EnvSession); it is masked like a tool's.
const EnvLoreSession = "LORE_SESSION"

// uuidShape matches a canonical UUID not glued to other hex digits on
// either side (a longer hex run is not a UUID). A dash or a letter
// prefix is fine ("T-<uuid>", "ses-<uuid>"): masking more is the safe
// side. Go regexp has no look-around, so the guard characters are
// captured and put back by Mask.
var uuidShape = regexp.MustCompile(`(^|[^0-9A-Fa-f])([0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12})($|[^0-9A-Fa-f])`)

// EnvNames lists every environment variable that carries a session id:
// each built-in detector's SessionEnv and LORE_SESSION, sorted, no
// duplicates. Only these names are read (AGENT_IDENTITY 9 allowlist).
func EnvNames() []string {
	names := []string{EnvLoreSession}
	for _, d := range agentdetect.Builtin() {
		names = append(names, d.SessionEnv...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// EnvIDs returns the session ids set in the environment read by getenv
// (EnvNames only), trimmed, empty values dropped.
func EnvIDs(getenv func(string) string) []string {
	var out []string
	for _, n := range EnvNames() {
		if v := strings.TrimSpace(getenv(n)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Mask returns text with every UUID-shaped token and every id in known
// (MinIDLen or longer) replaced by Redacted. Longer ids are replaced
// first, so an id that contains another is masked whole.
func Mask(text string, known []string) string {
	ids := make([]string, 0, len(known))
	for _, id := range known {
		if id = strings.TrimSpace(id); len(id) >= MinIDLen {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b string) int { return len(b) - len(a) })
	for _, id := range ids {
		text = strings.ReplaceAll(text, id, Redacted)
	}
	// Two adjacent UUIDs share one guard character, so one pass can skip
	// the second; repeat until nothing matches (each pass shrinks text).
	for uuidShape.MatchString(text) {
		text = uuidShape.ReplaceAllString(text, "${1}"+Redacted+"${3}")
	}
	return text
}

// MaskBytes is Mask over a byte slice (rendered JSON or text).
func MaskBytes(b []byte, known []string) []byte {
	return []byte(Mask(string(b), known))
}
