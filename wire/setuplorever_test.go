package wire

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
)

// TestPathLoreOlder: the PATH lore versus this binary across the version
// shapes lore builds carry (release, -dev, rc, git describe, dirty,
// snapshot, v prefix, unparsable, dev, hash).
func TestPathLoreOlder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		installed, binary string
		want              bool
	}{
		{"0.1.17", "0.1.18-dev", true},  // the owner's Mac
		{"0.1.17", "0.1.18", true},      // plain releases
		{"v0.1.17", "v0.1.18", true},    // v prefixes
		{"0.1.17", "0.2.0", true},       // minor
		{"0.9.9", "1.0.0", true},        // major
		{"0.1.18", "0.1.18", false},     // same release
		{"0.1.18", "0.1.18-dev", false}, // a pre-release is not past the release
		{"0.1.18", "0.1.18-rc1", false},
		{"0.1.18", "0.1.18-SNAPSHOT-abc1234", false},
		{"0.1.18", "0.1.18-3-gabc1234", true},       // describe past the tag
		{"0.1.18", "0.1.18-3-gabc1234-dirty", true}, // and dirty
		{"0.1.18", "0.1.18-dirty", true},            // dirty at the tag
		{"0.1.17", "0.1.17-34-gabc1234", true},
		{"0.1.19", "0.1.18-dev", false}, // installed is newer
		{"0.2.0", "0.1.99", false},
		{"0.1.10", "0.1.9", false}, // numeric, not lexical
		{"0.1.9", "0.1.10", true},
		{"0.1.17", "dev", false},          // unstamped build: no claim
		{"0.1.17", "", false},             // no version
		{"0.1.17", "abc1234", false},      // bare hash
		{"0.1.17", "0.1", false},          // two parts
		{"0.1.18-dev", "0.1.19", false},   // installed is not a release: not compared
		{"garbage", "0.1.18", false},      // unparsable installed
		{"", "0.1.18", false},             // empty installed
		{"0.1.17x", "0.1.18", false},      // trailing junk
		{"0.1.17", "0.1.18+build5", true}, // build metadata counts as a pre-release core
	}
	for _, c := range cases {
		if got := pathLoreOlder(c.installed, c.binary); got != c.want {
			t.Errorf("pathLoreOlder(%q, %q) = %v, want %v", c.installed, c.binary, got, c.want)
		}
	}
}

// TestParseLoreVersionOutput: the first line of `lore version`.
func TestParseLoreVersionOutput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		out, want string
		ok        bool
	}{
		{"lore 0.1.17\n  db_schema:        v1\n", "0.1.17", true},
		{"  lore v0.1.18-dev\n", "v0.1.18-dev", true},
		{"lore\n", "", false},
		{"", "", false},
		{"other 1.2.3\n", "", false},
		{"lore  \n", "", false},
	}
	for _, c := range cases {
		got, ok := parseLoreVersionOutput(c.out)
		if got != c.want || ok != c.ok {
			t.Errorf("parseLoreVersionOutput(%q) = %q, %v, want %q, %v", c.out, got, ok, c.want, c.ok)
		}
	}
}

// TestVersionRunDirWithTMPDIRGone: the PATH lore's version is asked in a
// folder that exists, also when TMPDIR names one that is gone.
func TestVersionRunDirWithTMPDIRGone(t *testing.T) {
	testenv.SetTemp(t, t.TempDir()+"/gone")
	if d, want := versionRunDir(), string(filepath.Separator); d != want {
		t.Errorf("run dir %q, want %q", d, want)
	}
}
