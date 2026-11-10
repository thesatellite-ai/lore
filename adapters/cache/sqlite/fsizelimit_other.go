//go:build !linux && !darwin

package sqlite

// fileSizeLimited has no probe on this OS: false, so an SQLite write error
// is never read as a full disk here.
func fileSizeLimited() bool { return false }
