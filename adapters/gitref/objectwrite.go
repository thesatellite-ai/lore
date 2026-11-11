package gitref

import (
	"context"
	"runtime"
	"strings"
	"time"
)

// retryObjectWrites turns on runObjectWrite's retry. Only Windows needs it:
// git finishes an object by renaming its temporary file onto
// objects/xx/yyyy, and while another git process (a concurrent lore write
// storing the same blob, "{}" say) has that file open, Windows refuses the
// rename and git fails with "unable to write file ./objects/…: Permission
// denied". POSIX renames never collide that way. A variable so a test can
// turn it on everywhere.
var retryObjectWrites = runtime.GOOS == "windows"

// objectWriteAttempts and objectWriteBackoff bound the retry: the other
// process holds the object only while it writes it, so a few short waits
// are enough; a real permission problem still fails within ~1.5 s.
const (
	objectWriteAttempts = 6
	objectWriteBackoff  = 50 * time.Millisecond
)

// runObjectWrite runs a git command that writes objects (hash-object -w,
// write-tree, commit-tree), retrying the transient Windows refusal above.
// Each of these commands is idempotent: running it again stores the same
// objects and prints the same ids.
func (r *Repo) runObjectWrite(ctx context.Context, env []string, stdin []byte, args ...string) (string, error) {
	out, err := r.run(ctx, env, stdin, args...)
	for attempt := 1; err != nil && retryObjectWrites && attempt < objectWriteAttempts && objectWriteRefused(Stderr(err)); attempt++ {
		select {
		case <-ctx.Done():
			return out, err
		case <-time.After(time.Duration(attempt) * objectWriteBackoff):
		}
		out, err = r.run(ctx, env, stdin, args...)
	}
	return out, err
}

// objectWriteRefused reports whether git's stderr is the refusal to store
// an object file (see retryObjectWrites).
func objectWriteRefused(stderr string) bool {
	return strings.Contains(stderr, "unable to write") && strings.Contains(stderr, "Permission denied")
}
