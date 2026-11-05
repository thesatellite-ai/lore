package metalist

import "github.com/thesatellite-ai/lore/engine"

// NoteLine is the human line of one write warning when it is a metadata
// note (engine.WarnMeta: a new key, a "stored as" line, a default, the
// size warning), "" for any other code. Every noun's add/edit Text prints
// these after its own line, the way `lore meta set` does (OV10), so a
// --meta given on add/edit tells the same as meta set.
func NoteLine(code, msg string) string {
	if code != string(engine.WarnMeta) || msg == "" {
		return ""
	}
	return msg + "\n"
}
