package wire

import "bytes"

// binarySniffLen is how many leading bytes decide whether a blob is
// binary: git's own rule (buffer_is_binary reads the first 8000 bytes and
// calls a file with a NUL byte in them binary), so lore and `git diff`
// agree on which staged file is text.
const binarySniffLen = 8000

// isBinaryDoc reports a staged or pushed file that is binary by git's rule
// (devco:Q-8). The secret scan skips it, as gitleaks does: its text rules
// read random bytes as lines, so a 20 MB asset took 15 s and its bytes
// matched password_pair at a "line" no person could read or fix. A commit
// message is never binary (its key is no path, its text has no NUL).
func isBinaryDoc(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), binarySniffLen)], 0) >= 0
}
