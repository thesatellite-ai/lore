package orgcfg

// gitmemo.go — answers this package's git reads give again without a git
// process (devco T-3).
//
// Every write in an org repo resolves the org's links (OrgResolution):
// for each store clone it resolved the branch tip, listed the tree and
// read every folder's _meta.json and lore-org.yaml at that tip, ~60 git
// processes per write. A task import of 650 rows ran them 650 times
// (over a minute of process starts). Two memos, both safe across a run:
//
//	immutable reads   `cat-file blob <commit>:<path>` and `ls-tree … <commit>`
//	                  name fixed content when <commit> is a full object id
//	branch tips       kept with what the local and tracking refs hold (read
//	                  from git's ref files): the tip, and the merge-base that
//	                  picks it, are a function of those two ids

import (
	"strings"
	"sync"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
)

// gitMemo holds the memoised answers of this process.
var gitMemo = struct {
	mu   sync.Mutex
	runs map[string]string  // immutable read argv -> stdout
	tips map[string]tipMemo // dir NUL branch -> tip
}{runs: map[string]string{}, tips: map[string]tipMemo{}}

// tipMemo is a branch tip with the ref-file fingerprint it was read under.
type tipMemo struct {
	fp, tip string
	ok      bool
}

// immutableRead reports whether args (as given to gitRun) only read
// content named by a full object id, so the answer can never change.
func immutableRead(args []string) bool {
	if len(args) < 4 || args[0] != "-C" {
		return false
	}
	cmd, rest := args[2], args[3:]
	last := rest[len(rest)-1]
	switch cmd {
	case "cat-file":
		if len(rest) != 2 || rest[0] != "blob" {
			return false
		}
		rev, _, ok := strings.Cut(last, ":")
		return ok && fullOID(rev)
	case "ls-tree":
		return fullOID(last)
	}
	return false
}

// fullOID reports whether s is a full sha1 or sha256 object id.
func fullOID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}

// memoKey is the memo key of argv.
func memoKey(args []string) string { return strings.Join(args, "\x00") }

// memoRun returns a memoised answer of args (ok=false: not held).
func memoRun(args []string) (string, bool) {
	gitMemo.mu.Lock()
	defer gitMemo.mu.Unlock()
	out, ok := gitMemo.runs[memoKey(args)]
	return out, ok
}

// keepRun memoises a successful immutable read.
func keepRun(args []string, out string) {
	gitMemo.mu.Lock()
	defer gitMemo.mu.Unlock()
	gitMemo.runs[memoKey(args)] = out
}

// refFingerprint is what branch's two refs (local and tracking) hold in
// the clone at dir, read from git's ref files (gitbackend.RefFromFiles):
// the tip is a pure function of them. "" when the files cannot answer
// (never memoised).
func refFingerprint(dir, branch string) string {
	local, ok := gitbackend.RefFromFiles(dir, "refs/heads/"+branch)
	if !ok {
		return ""
	}
	track, ok := gitbackend.RefFromFiles(dir, "refs/remotes/"+remoteName+"/"+branch)
	if !ok {
		return ""
	}
	return local + ":" + track
}

// memoTip returns the memoised tip of branch in dir while its ref files are
// unchanged.
func memoTip(dir, branch, fp string) (tipMemo, bool) {
	if fp == "" {
		return tipMemo{}, false
	}
	gitMemo.mu.Lock()
	defer gitMemo.mu.Unlock()
	t, ok := gitMemo.tips[dir+"\x00"+branch]
	return t, ok && t.fp == fp
}

// keepTip memoises branch's tip in dir under fingerprint fp.
func keepTip(dir, branch, fp, tip string, ok bool) {
	if fp == "" {
		return
	}
	gitMemo.mu.Lock()
	defer gitMemo.mu.Unlock()
	gitMemo.tips[dir+"\x00"+branch] = tipMemo{fp: fp, tip: tip, ok: ok}
}
