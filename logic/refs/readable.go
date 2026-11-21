package refs

import "strings"

// Readable is text as a person reads it: every reference name resolves is
// replaced by what it returns (a resource reference by the resource's
// name), every other reference stays exactly as written, and an escape
// "$${…}" reads "${…}". Why: an answer printed for an agent ("Saw
// ${res:person/alice-ng} about ${res:project/orbit}") reads as markup, and
// a small model summarising it keeps the names and drops the sentence
// around them (devco D-822); the stored text keeps its references (SQ3).
// A secret or env reference is never handed to name.
func Readable(text string, name func(Ref) (string, bool)) string {
	if !strings.Contains(text, open) {
		return text
	}
	var b strings.Builder
	for _, p := range Parse(text) {
		switch p.Kind {
		case PartEscaped:
			b.WriteString(strings.TrimPrefix(p.Raw, "$"))
		case PartRef:
			if p.Ref.Scheme.Expandable() && p.Ref.Problem == "" {
				if s, ok := name(p.Ref); ok {
					b.WriteString(s)
					continue
				}
			}
			b.WriteString(p.Raw)
		default:
			b.WriteString(p.Raw)
		}
	}
	return b.String()
}
