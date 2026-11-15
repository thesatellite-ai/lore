package gitfast

// persist.go — answers of expensive content-addressed reads (`log`,
// `grep`, `diff` over full object ids) kept across lore processes in the
// lore home's cache folder (<lore home>/cache/gitfast, SetCacheDir), so
// the store's history replay and the org checks do not re-run git on
// every command. Nothing is ever written inside a repository: a read
// leaves every repo and store clone as it was.
//
// Why it is safe: history behind a commit id never changes (replace refs,
// grafts and shallow repositories are refused by objectsPlain). What can
// change the printed answer for the same arguments is git's config and
// the git program itself, so both are in the key: the size and time of
// every config file git reads (system, global, XDG, the repository's) and
// of the git binary, with the repository's common dir.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// persistKeep is how many answers the cache folder keeps (the oldest
// go): each write moves a store's head, so older history answers are dead.
const persistKeep = 256

// persistMax is the largest answer kept (bytes).
const persistMax = 8 << 20

// persistable reports whether inv is a read whose answer depends only on
// object ids and its arguments: log with an explicit --format, grep, or
// diff, every revision a full object id.
func persistable(inv invocation, stdin []byte) bool {
	if len(inv.cflags) != 0 || len(stdin) != 0 {
		return false
	}
	switch inv.sub {
	case "log":
		hasFormat := false
		for _, a := range inv.args {
			if strings.HasPrefix(a, "--format=") || strings.HasPrefix(a, "--pretty=format:") {
				hasFormat = true
			}
		}
		if !hasFormat {
			return false
		}
	case "grep", "diff":
	default:
		return false
	}
	ids := 0
	expectValue := false
	for _, a := range inv.args {
		switch {
		case a == "--":
			return ids > 0
		case expectValue:
			expectValue = false
		case a == "-e" && inv.sub == "grep":
			expectValue = true
		case strings.HasPrefix(a, "-"):
			if a == "--output" || strings.HasPrefix(a, "--output=") || a == "--follow" || strings.HasPrefix(a, "--since") || strings.HasPrefix(a, "--until") || strings.HasPrefix(a, "--date") {
				return false
			}
		default:
			if !isFullID(strings.TrimPrefix(a, "^")) {
				return false
			}
			ids++
		}
	}
	// without "--" only diff of two full ids (git takes a 40-hex word for
	// an object id first); log and grep must end their revisions with "--"
	return inv.sub == "diff" && ids == 2
}

// persistPath is the file an answer of inv in r is kept in ("" = no cache).
func (s *Session) persistPath(r *repo, inv invocation) string {
	if s.cacheDir == "" {
		return ""
	}
	fp, ok := configFingerprint(r)
	if !ok {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(r.common))
	h.Write([]byte{0})
	h.Write([]byte(fp))
	h.Write([]byte{0})
	h.Write([]byte(inv.sub))
	if inv.literal {
		h.Write([]byte("\x00--literal-pathspecs"))
	}
	for _, a := range inv.args {
		h.Write([]byte{0})
		h.Write([]byte(a))
	}
	return filepath.Join(s.cacheDir, hex.EncodeToString(h.Sum(nil)))
}

// configFingerprint is the size and time of every file whose content can
// change a read's output: the config files git reads and the git binary.
func configFingerprint(r *repo) (string, bool) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return "", false
	}
	files := []string{bin, filepath.Join(r.common, "config")}
	if g := os.Getenv("GIT_CONFIG_GLOBAL"); g != "" {
		files = append(files, g)
	} else if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".gitconfig"))
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		files = append(files, filepath.Join(xdg, "git", "config"))
	}
	if os.Getenv("GIT_CONFIG_NOSYSTEM") == "" {
		files = append(files, systemConfig())
	}
	if os.Getenv("GIT_CONFIG_PARAMETERS") != "" || os.Getenv("GIT_CONFIG_COUNT") != "" {
		return "", false // config given in the environment: not fingerprinted
	}
	var b strings.Builder
	for _, f := range files {
		b.WriteString(f)
		if fi, err := os.Stat(f); err == nil {
			b.WriteString("\x00" + strconv.FormatInt(fi.Size(), 10) + "\x00" + strconv.FormatInt(fi.ModTime().UnixNano(), 10))
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", false
		}
		b.WriteByte('\n')
	}
	return b.String(), true
}

// systemConfig is git's system config file: $GIT_CONFIG_SYSTEM or the
// usual /etc/gitconfig (a git built with another prefix reads its own;
// that file is not fingerprinted, the binary's own stat stands for it).
func systemConfig() string {
	if p := os.Getenv("GIT_CONFIG_SYSTEM"); p != "" {
		return p
	}
	return "/etc/gitconfig"
}

// persistGet reads a kept answer: one status byte ('0' or '1'), then
// git's standard output.
func persistGet(path string) (Reply, bool) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) < 1 || (raw[0] != '0' && raw[0] != '1') {
		return Reply{}, false
	}
	return Reply{Out: raw[1:], Code: int(raw[0] - '0')}, true
}

// persistPut keeps an answer (status 0, or 1 for grep's "no match") and
// trims the folder to persistKeep.
func persistPut(path string, out []byte, code int) {
	if len(out) > persistMax || code < 0 || code > 1 {
		return
	}
	putFile(path, append([]byte{byte('0' + code)}, out...))
}

// putFile writes data to path atomically and trims the folder to
// persistKeep files (the oldest go). Failures only make lore slower.
func putFile(path string, data []byte) {
	out := data
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return // a cache that cannot be written is only slower
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(out)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name()) // best effort: a stray temp file is harmless
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= persistKeep {
		return
	}
	type aged struct {
		name string
		t    int64
	}
	var all []aged
	for _, e := range entries {
		if fi, err := e.Info(); err == nil {
			all = append(all, aged{e.Name(), fi.ModTime().UnixNano()})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t < all[j].t })
	for _, a := range all[:len(all)-persistKeep] {
		_ = os.Remove(filepath.Join(dir, a.name)) // best effort trim
	}
}
