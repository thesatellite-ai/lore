//go:build darwin

package rowfs

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// syncFile pushes f's bytes to the drive with a plain fsync(2). On macOS
// (*os.File).Sync is F_FULLFSYNC, which also empties the drive's whole
// write cache and costs milliseconds; a migration writing thousands of
// row files paid it per file (owner item 27: 400 s for 5,126 files). The
// cache flush is done once per folder instead, by syncDir (F_FULLFSYNC on
// the folder), which every write reaches after its renames: that flush
// covers every file fsync'd before it, so a batch is as durable as before.
func syncFile(f *os.File) error {
	for {
		err := unix.Fsync(int(f.Fd()))
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}
