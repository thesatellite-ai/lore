package gitfast

// configpersist.go — the config snapshot of a folder kept across lore
// processes (every hook and command is its own process, and each paid one
// `git config --list` for it). The snapshot is valid while every file it
// was read from (the --show-origin of each entry, plus the standard files
// git would read even when they did not exist yet) and the git binary
// keep their size and time, and the environment that picks config files
// is the same. A snapshot that depends on the branch (includeIf onbranch:)
// is never kept. Kept in the Session's cache folder, never in a repository.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// persistedConfig is the file format of a kept snapshot.
type persistedConfig struct {
	Env     string        `json:"env"`
	Files   []string      `json:"files"`
	Stats   []string      `json:"stats"`
	Entries []configEntry `json:"-"`
	Raw     [][4]string   `json:"entries"` // scope, key, value, "1" when it has a value
}

// configEnvNames are the variables that choose which config files git reads.
var configEnvNames = []string{"HOME", "XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "PATH"}

// configEnv is the fingerprint of those variables.
func configEnv() string {
	var b strings.Builder
	for _, n := range configEnvNames {
		b.WriteString(n + "=" + os.Getenv(n) + "\x00")
	}
	return b.String()
}

// standardConfigFiles are the files git reads for r whether or not they
// exist (a new one must invalidate a kept snapshot); r nil is a folder
// outside every repository (no repository files).
func standardConfigFiles(r *repo) []string {
	var files []string
	if r != nil {
		files = append(files, filepath.Join(r.common, "config"), filepath.Join(r.gitDir, "config.worktree"))
	}
	if bin, err := exec.LookPath("git"); err == nil {
		files = append(files, bin)
	}
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
	return files
}

// statOf is "size/mtime" of p, or "-" when it does not exist.
func statOf(p string) (string, bool) {
	fi, err := os.Stat(p)
	if os.IsNotExist(err) {
		return "-", true
	}
	if err != nil {
		return "", false
	}
	return strconv.FormatInt(fi.Size(), 10) + "/" + strconv.FormatInt(fi.ModTime().UnixNano(), 10), true
}

// configPersistPath is where dir's snapshot is kept ("" = nowhere).
// noRepo names the snapshot of a folder outside every repository apart
// from the one of a repository made there later: the plain folder's
// fingerprint has no repository config file to change.
func (s *Session) configPersistPath(dir, cflagsKey string, noRepo bool) string {
	if s.cacheDir == "" {
		return ""
	}
	kind := "config"
	if noRepo {
		kind = "config-norepo"
	}
	h := sha256.Sum256([]byte(kind + "\x00" + dir + "\x00" + cflagsKey))
	return filepath.Join(s.cacheDir, "config-"+hex.EncodeToString(h[:]))
}

// loadPersistedConfig returns dir's kept snapshot when still valid.
func loadPersistedConfig(path string) (*configSnap, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var p persistedConfig
	if json.Unmarshal(raw, &p) != nil || p.Env != configEnv() || len(p.Files) != len(p.Stats) {
		return nil, false
	}
	for i, f := range p.Files {
		st, ok := statOf(f)
		if !ok || st != p.Stats[i] {
			return nil, false
		}
	}
	snap := &configSnap{}
	for _, e := range p.Raw {
		snap.entries = append(snap.entries, configEntry{scope: e[0], key: e[1], value: e[2], hasValue: e[3] == "1"})
	}
	return snap, true
}

// savePersistedConfig keeps snap with the files it came from.
func savePersistedConfig(path string, r *repo, snap *configSnap, origins []string) {
	for _, e := range snap.entries {
		if strings.HasPrefix(e.key, "includeif.onbranch:") {
			return // depends on the checked-out branch
		}
	}
	p := persistedConfig{Env: configEnv()}
	seen := map[string]bool{}
	for _, f := range append(standardConfigFiles(r), origins...) {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		st, ok := statOf(f)
		if !ok {
			return
		}
		p.Files = append(p.Files, f)
		p.Stats = append(p.Stats, st)
	}
	for _, e := range snap.entries {
		has := ""
		if e.hasValue {
			has = "1"
		}
		p.Raw = append(p.Raw, [4]string{e.scope, e.key, e.value, has})
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return
	}
	putFile(path, raw)
}
