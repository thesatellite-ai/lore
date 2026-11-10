package host

// nountable.go — which table a command noun or a row id names, for the
// live answers that print a table (the not-found `lore sync peek` hint)
// or filter by one (`lore land --kind`). Read from the compiled-in table
// definitions (schema.Builtin), never guessed by adding an "s": that
// guess named tables that do not exist (techdoc → "techdocs", not
// tech_docs; architecturenote, cookbookrecipe, tastepref the same).

import (
	"strings"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
)

// tableOfNoun is the table a command noun stands for: the singular noun
// ("techdoc", "memory", "hotfix", "tasklist") or the table name itself
// ("tech_docs", "tasks"), matched against every built-in table with its
// underscores dropped (noun + "s", + "es", y → "ies"). ok is false for a
// noun no built-in table answers (a dynamic kind, a typo).
func tableOfNoun(noun string) (string, bool) {
	n := strings.ToLower(strings.TrimSpace(noun))
	if n == "" {
		return "", false
	}
	flat := strings.ReplaceAll(n, "_", "")
	forms := []string{flat + "s", flat + "es"}
	if strings.HasSuffix(flat, "y") {
		forms = append(forms, strings.TrimSuffix(flat, "y")+"ies")
	}
	for _, t := range schema.Builtin() {
		if !t.Kind.IsZero() {
			continue
		}
		name := string(t.Name)
		if name == n {
			return name, true
		}
		joined := strings.ReplaceAll(name, "_", "")
		for _, f := range forms {
			if joined == f {
				return name, true
			}
		}
	}
	return "", false
}

// tableOfID is the built-in table whose ids carry id's prefix; ok is
// false for a prefix no built-in table has.
func tableOfID(id string) (string, bool) {
	p, _, ok := strings.Cut(strings.TrimSpace(id), "_")
	if !ok {
		return "", false
	}
	for _, t := range schema.Builtin() {
		if t.Kind.IsZero() && t.Prefix == ids.Prefix(p) {
			return string(t.Name), true
		}
	}
	return "", false
}
