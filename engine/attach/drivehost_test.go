package attach

import "testing"

// TestCleanURLKeepsTheDrive: a Windows path written after file://
// (file://C:/Users/a/kb) is the drive path; before, "C:" was read as the
// url's host and the store's folder lost its drive (`\Users\a\kb`).
func TestCleanURLKeepsTheDrive(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"file://C:/Users/a/kb":  "file:///C:/Users/a/kb",
		"file:///C:/Users/a/kb": "file:///C:/Users/a/kb",
		"file:///srv/kb":        "file:///srv/kb",
	} {
		got, err := CleanURL(in)
		if err != nil || got != want {
			t.Errorf("CleanURL(%s) = %s, %v; want %s", in, got, err, want)
		}
	}
}
