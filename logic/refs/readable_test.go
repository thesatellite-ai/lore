package refs

import "testing"

// TestReadable (devco D-822): a reference name resolves reads as the
// name, any other one stays as written, an escape reads as the literal
// text, and a secret or env reference is never handed to the resolver.
func TestReadable(t *testing.T) {
	asked := map[Scheme]bool{}
	name := func(r Ref) (string, bool) {
		asked[r.Scheme] = true
		if r.Scheme == SchemeRes && r.Target == "person/alice-ng" {
			return "alice-ng", true
		}
		return "", false
	}
	for in, want := range map[string]string{
		"plain text": "plain text",
		"Saw ${res:person/alice-ng} about ${res:project/orbit}":     "Saw alice-ng about ${res:project/orbit}",
		"keep $${res:person/alice-ng} as written":                   "keep ${res:person/alice-ng} as written",
		"${secret:gopass://a/b} ${env:HOME} ${res:person/alice-ng}": "${secret:gopass://a/b} ${env:HOME} alice-ng",
		"broken ${res:person/alice-ng":                              "broken ${res:person/alice-ng",
	} {
		if got := Readable(in, name); got != want {
			t.Errorf("Readable(%q) = %q, want %q", in, got, want)
		}
	}
	if asked[SchemeSecret] || asked[SchemeEnv] {
		t.Errorf("a secret or env reference was handed to the resolver: %v", asked)
	}
}
