// context_hookseen.go — the context hook's per-session seen set
// (CONTEXT_BUDGET 6.9): the PreToolUse hook skips row ids it already sent
// in this tool session, so opening ten files in one folder does not repeat
// the same rules.
//
//	runContextHook
//	  ├─ loadHookSeen(tool, session_id) ─► ~/.lore/state/hook-seen/<tool>-<sha256(session)[:16]>.ids
//	  ├─ contextResult.without(seen)     (nothing new → print nothing)
//	  └─ seen.add(printed ids)           append-only, one id per line
//
// Machine-local on purpose (like the session rows, OQ14): the file holds
// only row ids, never text, and the session id is hashed in the name. A
// hook input without a session id has no set: every call answers in full.
// Files older than hookSeenTTL are removed on the next write.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// hookSeenDir is the folder of the seen sets under ~/.lore.
const hookSeenDir = "state/hook-seen"

// hookSeenTTL: a set older than this belongs to a session that ended.
const hookSeenTTL = 7 * 24 * time.Hour

// hookSeenHashLen is how many hex characters of the session hash name the file.
const hookSeenHashLen = 16

// hookSeen is one session's seen set; file is "" when there is no session.
type hookSeen struct {
	file string
	ids  map[string]bool
}

// hookSeenFile is the set's path, "" without a session id or home folder.
func hookSeenFile(tool, session string) string {
	session = strings.TrimSpace(session)
	if session == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(tool + "\x00" + session))
	return filepath.Join(home, ".lore", filepath.FromSlash(hookSeenDir), tool+"-"+hex.EncodeToString(sum[:])[:hookSeenHashLen]+".ids")
}

// loadHookSeen reads the set; a missing or unreadable file is an empty set.
func loadHookSeen(tool, session string) hookSeen {
	s := hookSeen{file: hookSeenFile(tool, session), ids: map[string]bool{}}
	if s.file == "" {
		return s
	}
	fh, err := os.Open(s.file)
	if err != nil {
		return s
	}
	defer func() { _ = fh.Close() }() // read-only file
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		if id := strings.TrimSpace(sc.Text()); id != "" {
			s.ids[id] = true
		}
	}
	return s
}

// add appends ids to the set (no-op without a session) and prunes old sets.
func (s hookSeen) add(ids []string) error {
	if s.file == "" || len(ids) == 0 {
		return nil
	}
	dir := filepath.Dir(s.file)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	pruneHookSeen(dir, s.file)
	fh, err := os.OpenFile(s.file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, id := range ids {
		if !s.ids[id] {
			s.ids[id] = true
			b.WriteString(id + "\n")
		}
	}
	if _, err = fh.WriteString(b.String()); err != nil {
		_ = fh.Close() // the write error is the one reported
		return err
	}
	return fh.Close()
}

// pruneHookSeen removes sets older than hookSeenTTL except keep; best effort.
func pruneHookSeen(dir, keep string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cut := cliClock().Now().Add(-hookSeenTTL)
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		if p == keep || e.IsDir() || !strings.HasSuffix(e.Name(), ".ids") {
			continue
		}
		if info, ierr := e.Info(); ierr == nil && info.ModTime().Before(cut) {
			_ = os.Remove(p) // best effort: a stale set only costs disk
		}
	}
}
