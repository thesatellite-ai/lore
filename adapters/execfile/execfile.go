// Package execfile writes files that will be executed (a git hook, a new
// lore binary) so that running them can never fail with ETXTBSY ("text file
// busy") because of a fork in this same process.
//
// Why: Linux refuses to exec a file that some process holds open for
// writing. os.WriteFile opens the file O_CLOEXEC, but when another goroutine
// forks (any exec.Cmd: lore runs git from many goroutines in a live session,
// `lore api` and the daemons) between that open and the close, the child
// holds a copy of the write descriptor until it reaches its own exec. An
// exec of the written file in that window fails: a hook git cannot run, or
// `lore upgrade`'s smoke test of the new binary refused.
//
// Fix at the cause, no retry: Do holds syscall.ForkLock for reading while
// the file is open. The runtime takes that lock for writing around every
// fork, so no fork of this process can happen while the descriptor is open
// and no child can inherit it. Other processes never share this process's
// descriptors, so they cannot cause the race. The cost is that forks of this
// process wait for the write to finish.
package execfile

import (
	"io/fs"
	"os"
	"syscall"
)

// Do runs write, which opens, writes and closes a file that will be
// executed, with no fork of this process able to inherit its descriptor.
// write must close every descriptor it opens before it returns and must not
// itself start a process (that would deadlock on the fork lock).
func Do(write func() error) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return write()
}

// WriteFile is os.WriteFile under Do, then sets mode even on a file that
// already existed (os.WriteFile applies mode only to a file it creates).
func WriteFile(path string, data []byte, mode fs.FileMode) error {
	return Do(func() error {
		if err := os.WriteFile(path, data, mode); err != nil {
			return err
		}
		return os.Chmod(path, mode)
	})
}
