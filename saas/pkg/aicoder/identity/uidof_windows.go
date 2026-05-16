//go:build windows

package identity

import (
	"errors"
	"os"
)

// uidOf is a no-op on Windows — UID concept doesn't map cleanly. CheckPrivilege
// skips the EUID-vs-HOME-owner check when this returns an error.
func uidOf(fi os.FileInfo) (int, error) {
	_ = fi
	return 0, errors.New("identity: file ownership check unsupported on Windows")
}
