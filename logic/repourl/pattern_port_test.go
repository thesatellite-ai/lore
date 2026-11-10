package repourl

// The v0.1.17 TestCompileRefuses (saas/pkg/aicoder/repourl
// repourl_test.go), ported before phase E: the root TestCompileRefuses
// kept five of its seven refusals; this adds the two it dropped (a host
// with no path, an empty path segment) and the zero Pattern that must
// match nothing, so an org's own:/repos: list never matches by accident.

import "testing"

func TestCompileRefusesV017Matrix(t *testing.T) {
	for _, b := range []string{
		"",
		"github.com",         // no path
		"*/acme/app",         // wildcard host matches every company
		"**/app",             // same
		"github.com/acme*/x", // partial wildcard
		"github.com/**/a/**", // two **
		"github.com//app",    // empty segment
	} {
		if _, err := Compile(b); err == nil {
			t.Errorf("Compile(%q) accepted, want error", b)
		}
	}
	var zero Pattern
	if zero.Match("github.com/acme/app") {
		t.Error("zero Pattern must match nothing")
	}
}
