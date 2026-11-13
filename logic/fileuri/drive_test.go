package fileuri

import "testing"

// TestIsDrivePath (B3-27): a drive path is a local path on every OS, in
// either separator; a drive letter alone, a url scheme and a relative
// name are not.
func TestIsDrivePath(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{
		`C:\Users\nina\lore-notes.git`: true,
		`C:/Users/nina/lore-notes.git`: true,
		`d:\x`:                         true,
		`C:`:                           false,
		`C:x`:                          false,
		`ab:/x`:                        false,
		`ssh://host/x`:                 false,
		`git@host:o/r`:                 false,
		`/srv/x.git`:                   false,
	} {
		if got := IsDrivePath(in); got != want {
			t.Errorf("IsDrivePath(%q) = %v, want %v", in, got, want)
		}
	}
	if got := DriveURLPath(`C:\Users\nina\x.git`); got != "/C:/Users/nina/x.git" {
		t.Errorf("DriveURLPath = %q", got)
	}
	if !IsDriveURLPath("/C:/x") || IsDriveURLPath("/srv/x") || IsDriveURLPath("C:/x") {
		t.Error("IsDriveURLPath misreads a url path")
	}
}
