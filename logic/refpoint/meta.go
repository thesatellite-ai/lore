package refpoint

import (
	"encoding/json"
	"strings"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/row"
)

// metaPathSep / metaKeySep split a ${meta:…} target into segments
// ("smtp/url" or "smtp.url"); a dotted key whose leading segments name an
// object entry reads the rest inside that object (DECISION META-B7-OBJ:
// an object is one json value).
const (
	metaPathSep = "/"
	metaKeySep  = "."
)

// KeyFlags answers whether the key registry marks a metadata key sensitive
// (METADATA 3.20 "one masker": a value is masked when its entry's label OR
// the registry says so). An entry written before its key turned sensitive
// carries no label, so a caller that prints values must supply the
// registry's view; the logic layer cannot read it itself.
type KeyFlags interface {
	KeySensitive(keyID string) bool
}

// KeyFlagsFunc adapts a function to KeyFlags (a caller wraps its own key
// row lookup).
type KeyFlagsFunc func(keyID string) bool

// KeySensitive implements KeyFlags.
func (f KeyFlagsFunc) KeySensitive(keyID string) bool { return f != nil && f(keyID) }

// MetaOf is MetaOfWith without a registry: only entry labels mask. It
// stays until both callers (ops/internal/expand, saas/cmd/cli
// render_private) pass their registry; new code uses MetaOfWith.
func MetaOf(r row.Row) func(key string) (MetaValue, bool) { return MetaOfWith(r, nil) }

// MetaOfWith is Options.Meta for r: each stored meta entry carries its key
// name, sensitive flag and value (logic/meta Entry); flags (nil: none) adds
// the registry's sensitive flag by key id (3.20). The longest entry name
// that is a prefix of the target wins; the remaining segments walk into its
// JSON object. A string prints unquoted; any other value prints as its
// JSON. An entry that does not decode is skipped (not filled).
func MetaOfWith(r row.Row, flags KeyFlags) func(key string) (MetaValue, bool) {
	type keyed struct {
		id string
		e  meta.Entry
	}
	return func(key string) (MetaValue, bool) {
		segs := strings.Split(strings.ReplaceAll(key, metaPathSep, metaKeySep), metaKeySep)
		entries := map[string]keyed{}
		for kid, j := range r.Meta {
			if e, err := meta.DecodeEntry(j); err == nil {
				entries[e.Name] = keyed{id: kid.String(), e: e}
			}
		}
		for n := len(segs); n > 0; n-- {
			k, ok := entries[strings.Join(segs[:n], metaKeySep)]
			if !ok {
				continue
			}
			sensitive := k.e.Sensitive || (flags != nil && flags.KeySensitive(k.id))
			raw := json.RawMessage(k.e.V)
			for _, s := range segs[n:] {
				var obj map[string]json.RawMessage
				if json.Unmarshal(raw, &obj) != nil {
					return MetaValue{}, false
				}
				if raw, ok = obj[s]; !ok {
					return MetaValue{}, false
				}
			}
			var str string
			if json.Unmarshal(raw, &str) == nil {
				return MetaValue{Text: str, Sensitive: sensitive}, true
			}
			return MetaValue{Text: string(raw), Sensitive: sensitive}, true
		}
		return MetaValue{}, false
	}
}
