package stores

import (
	"errors"
	"os/exec"
	"testing"
)

// branchCases is the input space for ValidateBranch: every git ref-name rule
// that can apply to one segment, plus lore's own "no slash" rule and names
// that must stay valid.
var branchCases = []struct {
	name string
	ok   bool
}{
	{"main", true}, {"staging", true}, {"release-1.2", true}, {"a.b", true},
	{"feature_x", true}, {"a@b", true}, {"a{b", true}, {"é-unicode", true},
	{"", false}, {".", false}, {"..", false}, {"-x", false}, {".hidden", false},
	{"trailing.", false}, {"x.lock", false}, {"a..b", false}, {"a@{b", false},
	{"a/b", false}, {"a b", false}, {"a\tb", false}, {"a~b", false}, {"a^b", false},
	{"a:b", false}, {"a?b", false}, {"a*b", false}, {"a[b", false}, {"a\\b", false},
	{"a\x01b", false}, {"a\x1fb", false}, {"a\x7fb", false}, {"\x00", false},
	{"HEAD", false}, {"@", false},
}

func TestValidateBranch_Rules(t *testing.T) {
	t.Parallel()
	for _, tc := range branchCases {
		err := ValidateBranch(tc.name)
		if tc.ok && err != nil {
			t.Errorf("ValidateBranch(%q) = %v, want ok", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, ErrBadBranch) {
			t.Errorf("ValidateBranch(%q) = %v, want ErrBadBranch", tc.name, err)
		}
	}
}

// TestValidateBranch_NeverLooserThanGit checks the promise against the real
// git: every name ValidateBranch accepts, `git check-ref-format --branch`
// accepts too. ("@" is the one name lore refuses on purpose although git
// allows it; refusing more is safe, accepting more is the bug.) Skips only
// when git is not installed; NUL cannot be passed as an argument.
func TestValidateBranch_NeverLooserThanGit(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, tc := range branchCases {
		if tc.name == "\x00" || ValidateBranch(tc.name) != nil {
			continue
		}
		cmd := exec.Command("git", "check-ref-format", "--branch", tc.name)
		cmd.Dir = t.TempDir() // outside any repo: no @{-N} expansion
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("lore accepts %q but git refuses it: %s", tc.name, out)
		}
	}
}
