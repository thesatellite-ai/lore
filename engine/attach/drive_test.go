package attach

import "testing"

// TestCleanURLDrivePath (B3-27): a Windows drive path store url is read as
// a local path ("/C:/…"), never as a url with scheme "c".
func TestCleanURLDrivePath(t *testing.T) {
	t.Parallel()
	for _, in := range []string{`C:\Users\nina\notes.git`, `C:/Users/nina/notes.git`} {
		for name, f := range map[string]func(string) (string, error){"CleanURL": CleanURL, "NormalizeURL": NormalizeURL} {
			if got, err := f(in); err != nil || got != "/C:/Users/nina/notes.git" {
				t.Errorf("%s(%q) = %q, %v", name, in, got, err)
			}
		}
	}
}
