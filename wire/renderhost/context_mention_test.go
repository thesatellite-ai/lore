package renderhost

import "testing"

// TestMentionsPathIsWholePath guards devco D-574: `context --for-file
// main.go` returned a rule about another repo's saas/cmd/cron/main.go.
func TestMentionsPathIsWholePath(t *testing.T) {
	cases := []struct {
		body, p string
		want    bool
	}{
		{"the cron entry point saas/cmd/cron/main.go must stay small", "main.go", false},
		{"edit main.go first", "main.go", true},
		{"see main.go.", "main.go", true},
		{"main.go", "main.go", true},
		{"(main.go)", "main.go", true},
		{"`main.go` holds the flags", "main.go", true},
		{"main.gox is generated", "main.go", false},
		{"keep main.go.bak", "main.go", false},
		{"saas/cmd/cron/main.go then main.go", "main.go", true},
		{"saas/cmd/cron/main.go is the job", "saas/cmd/cron/main.go", true},
		{"vendor/saas/cmd/cron/main.go", "saas/cmd/cron/main.go", false},
		{"under cmd/ only", "cmd/", true},
		{"under saas/cmd/ only", "cmd/", false},
		{"", "main.go", false},
	}
	for _, c := range cases {
		if got := mentionsPath(c.body, c.p); got != c.want {
			t.Errorf("mentionsPath(%q, %q) = %v, want %v", c.body, c.p, got, c.want)
		}
	}
}
