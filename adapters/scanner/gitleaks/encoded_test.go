package gitleaks

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/zricethezav/gitleaks/v8/detect/codec"
)

// segmentsIn is the number of segments gitleaks' codec finds in one pass.
func segmentsIn(text string) int {
	_, segs := codec.NewDecoder().Decode(text, nil)
	return len(segs)
}

// TestMayHoldEncodingCases pins each branch of the prefilter at its
// boundary (one byte short of a candidate is no candidate).
func TestMayHoldEncodingCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"empty", "", false},
		{"prose", "Rotate the deploy token after the release; the password policy lives here.", false},
		{"run of 15", strings.Repeat("a", 15), false},
		{"run of 16", strings.Repeat("a", 16), true},
		{"run of 15 split by a space", strings.Repeat("a", 8) + " " + strings.Repeat("a", 8), false},
		{"run with / + - _", "ab/cd+ef-gh_ij/kl", true},
		{"run broken by a dot", "abcdefgh.ijklmnop", false},
		{"run broken by non-ASCII", "abcdefghéijklmnop", false},
		{"hex 32", strings.Repeat("af09", 8), true},
		{"percent", "a%41b", true},
		{"percent lower hex", "a%2fb", true},
		{"percent one digit", "a%4", false},
		{"percent not hex", "a%G1", false},
		{"percent at end", "100%", false},
		{"U+ code point", "x U+0041 y", true},
		{"lower u+", "x u+0041 y", false},
		{"plus alone", "a+b", false},
		{"backslash u", `x \u0041 y`, true},
		{"backslash U", `x \U0041 y`, true},
		{"double backslash u", `x \\u0041`, true},
		{"backslash n", `x \n y`, false},
		{"backslash at end", `x \`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mayHoldEncoding(c.text); got != c.want {
				t.Fatalf("mayHoldEncoding = %v, want %v", got, c.want)
			}
		})
	}
}

// encodedCorpus is texts across the codec's encodings, near misses and
// noise; every one must keep the superset invariant.
func encodedCorpus() []string {
	secret := "ACMETOK-" + strings.Repeat("7", 12)
	out := []string{
		"", "plain words only", "key_1:\"owner review\"", "a%41", "%zz%4", "U+004", "U+0041", `\u004`, `\u0041`,
		base64.StdEncoding.EncodeToString([]byte(secret)), hex.EncodeToString([]byte(secret)), percentAll(secret),
		"short/path/x", "a-b-c-d-e-f-g-h", "x=" + strings.Repeat("Z", 15), "x=" + strings.Repeat("Z", 16),
		base64.StdEncoding.EncodeToString([]byte("hello world!")), "x " + percentAll("hi") + " y",
		"cafécafécafécafécafé", "line1\nline2\tline3",
	}
	// Mixed strings over an alphabet weighted toward the codec's bytes, so
	// short candidates and near misses both occur. The bytes come from a
	// fixed arithmetic walk (reproducible, no random source).
	const alpha = "aZ09_/+-%U\\u. =\n\u00e9fF"
	for i := range 4000 {
		n := (i * 7) % 41
		var b strings.Builder
		for j := range n {
			b.WriteByte(alpha[(i*7919+j*j*31+i*j)%len(alpha)])
		}
		out = append(out, b.String())
	}
	return out
}

// TestMayHoldEncodingIsASuperset: whenever the prefilter says no, the
// codec finds no segment either (the invariant that makes skipping the
// decode passes change no finding). Fails when a gitleaks upgrade widens
// the codec's patterns past the prefilter.
func TestMayHoldEncodingIsASuperset(t *testing.T) {
	t.Parallel()
	for i, text := range encodedCorpus() {
		if !mayHoldEncoding(text) && segmentsIn(text) != 0 {
			t.Fatalf("corpus %d (len %d): prefilter says no encoding, codec found %d segment(s)", i, len(text), segmentsIn(text))
		}
	}
}

// FuzzMayHoldEncodingIsASuperset is the same invariant over fuzzed input
// (seeds run in every go test).
func FuzzMayHoldEncodingIsASuperset(f *testing.F) {
	for _, s := range encodedCorpus()[:20] {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if !mayHoldEncoding(text) && segmentsIn(text) != 0 {
			t.Fatalf("prefilter says no encoding, codec found %d segment(s)", segmentsIn(text))
		}
	})
}

// TestPlainDetectorSettings: the decode-off twin has the 3.1 settings
// and depth 0, beside the depth-3 detector (TestDetectorSettings).
func TestPlainDetectorSettings(t *testing.T) {
	t.Parallel()
	s := full(t)
	if s.plain == nil || !s.plain.IgnoreGitleaksAllow || s.plain.MaxDecodeDepth != 0 || s.detector.MaxDecodeDepth != maxDecodeDepth {
		t.Fatalf("plain detector settings: %+v", s.plain)
	}
}

// TestPrefilterKeepsFindings: the full scanner (gitleaks defaults + lore
// pack) reports the same findings as a scan with the decode passes forced
// on, over texts with and without encoded segments, plain and encoded
// secrets (a gitleaks-only rule and a lore rule).
func TestPrefilterKeepsFindings(t *testing.T) {
	t.Parallel()
	s := full(t)
	glpat := "glpat-" + "Xk9Lm2Qp7Rt4Vw8Yz1Ab"
	texts := []string{
		"plain words only",
		"export TOKEN=" + glpat,
		"note " + base64.StdEncoding.EncodeToString([]byte("token="+glpat)) + " end",
		"note " + percentAll("token="+glpat) + " end",
		strings.Repeat("Rotate the deploy token after the release. ", 40),
	}
	for i, text := range texts {
		got := ruleIDs(s.Scan("body", text))
		var forced []string
		for _, f := range s.detector.DetectString(text) {
			forced = append(forced, f.RuleID)
		}
		for _, id := range forced {
			if !got[id] {
				t.Errorf("text %d: rule %s found with decoding forced, missing from Scan", i, id)
			}
		}
		if wantPat := i >= 1 && i <= 3; got["gitlab-pat"] != wantPat {
			t.Errorf("text %d: gitlab-pat found = %v, want %v", i, got["gitlab-pat"], wantPat)
		}
	}
}
