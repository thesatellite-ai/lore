package wire

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// resolveSetupPath is the production setup-folder resolver: symlinks
// resolved, and on macOS every name spelled as it is on disk. macOS's file
// system ignores case, and git names a repo by its on-disk case, so a
// setup folder written ~/code/macapp never matched the repo
// ~/code/MacApp. (Windows' EvalSymlinks already returns the on-disk case.)
func resolveSetupPath(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		r = realCase(r)
	}
	return r, nil
}

// realCase spells each name of the absolute path p as its folder lists it,
// when the exact name is not listed but one differing only in case is (a
// case-insensitive file system). Names it cannot find are kept.
func realCase(p string) string {
	vol := filepath.VolumeName(p)
	rest := strings.TrimPrefix(p[len(vol):], string(filepath.Separator))
	if rest == "" {
		return p
	}
	cur := vol + string(filepath.Separator)
	parts := strings.Split(rest, string(filepath.Separator))
	for i, name := range parts {
		names, err := dirNames(cur)
		if err != nil {
			return filepath.Join(append([]string{cur}, parts[i:]...)...)
		}
		pick := ""
		for _, n := range names {
			if n == name {
				pick = n
				break
			}
			if pick == "" && strings.EqualFold(n, name) {
				pick = n
			}
		}
		if pick == "" {
			pick = name
		}
		cur = filepath.Join(cur, pick)
	}
	return cur
}

func dirNames(dir string) ([]string, error) {
	f, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read only
	return f.Readdirnames(-1)
}
