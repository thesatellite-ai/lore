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

// MetaOf is Options.Meta for r: each stored meta entry carries its key
// name, sensitive flag and value (logic/meta Entry), so no key lookup is
// needed. The longest entry name that is a prefix of the target wins; the
// remaining segments walk into its JSON object. A string prints unquoted;
// any other value prints as its JSON. An entry that does not decode is
// skipped (not filled).
func MetaOf(r row.Row) func(key string) (MetaValue, bool) {
	return func(key string) (MetaValue, bool) {
		segs := strings.Split(strings.ReplaceAll(key, metaPathSep, metaKeySep), metaKeySep)
		entries := map[string]meta.Entry{}
		for _, j := range r.Meta {
			if e, err := meta.DecodeEntry(j); err == nil {
				entries[e.Name] = e
			}
		}
		for n := len(segs); n > 0; n-- {
			e, ok := entries[strings.Join(segs[:n], metaKeySep)]
			if !ok {
				continue
			}
			raw := json.RawMessage(e.V)
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
				return MetaValue{Text: str, Sensitive: e.Sensitive}, true
			}
			return MetaValue{Text: string(raw), Sensitive: e.Sensitive}, true
		}
		return MetaValue{}, false
	}
}
