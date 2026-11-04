//go:build !windows

package rowfs

import (
	"errors"
	"os"
	"path/filepath"
)

// syncDir fsyncs a directory so a rename into it survives a power loss.
func syncDir(dir string) error {
	d, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
