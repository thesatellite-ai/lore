package wire

// hometidy.go — lore's home keeps nothing without a reason (plain files
// sweep 3.2, 2026-11-12). Every command that opens the machine store
// tidies what lore itself left behind, by age, and `lore doctor` names
// what only a person should remove:
//
//	tidyHome   tmp/ entries older than a day (a probe or lens killed
//	           mid-run), then tmp/ itself when empty; damaged cache copies
//	           (*.corrupt-*) older than a week; half-written temp files in
//	           the home older than an hour
//	doctor     home_leftovers: the folders and files of earlier builds
//	           (worktrees/, pii-patterns.txt.migrated) with the command that
//	           removes each, and the damaged cache copies kept; old clones
//	           (clones/, clones-v017/) are doctor's old_clones check, per clone

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// How long lore keeps what it left behind.
const (
	tmpKeep     = 24 * time.Hour
	corruptKeep = 7 * 24 * time.Hour
	halfKeep    = time.Hour
)

// halfWrittenPatterns are the temp files of the home's atomic writes.
var halfWrittenPatterns = []string{".config.yaml.tmp-*", ".api-token-*", ".README.md.tmp-*"}

// tidyHome removes what lore left behind in home (see the file comment).
// Best effort: what cannot be removed stays and `lore where` names it.
func tidyHome(home string, now time.Time) {
	if home == "" {
		return
	}
	tmp := filepath.Join(home, probeTmpDir)
	if ents, err := os.ReadDir(tmp); err == nil {
		left := len(ents)
		for _, e := range ents {
			if info, ierr := e.Info(); ierr == nil && now.Sub(info.ModTime()) > tmpKeep {
				if os.RemoveAll(filepath.Join(tmp, e.Name())) == nil {
					left--
				}
			}
		}
		if left == 0 {
			_ = os.Remove(tmp) // an empty scratch folder has no reason to stay
		}
	}
	for _, p := range halfWrittenPatterns {
		matches, _ := filepath.Glob(filepath.Join(home, p))
		for _, m := range matches {
			if info, err := os.Stat(m); err == nil && now.Sub(info.ModTime()) > halfKeep {
				_ = os.Remove(m) // best effort, see above
			}
		}
	}
	for _, c := range corruptCopies(home) {
		if info, err := os.Stat(c); err == nil && now.Sub(info.ModTime()) > corruptKeep {
			_ = os.Remove(c) // best effort, see above
		}
	}
}

// corruptCopies are the damaged cache files moved aside under the home's
// cache folder.
func corruptCopies(home string) []string {
	var out []string
	_ = filepath.WalkDir(filepath.Join(home, cacheDir), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(d.Name(), corruptSuffix) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// DoctorCheckHomeLeftovers names the doctor check.
const DoctorCheckHomeLeftovers DoctorCheck = "home_leftovers"

// homeLeftovers are the entries of earlier builds a person removes, with
// what they are.
var homeLeftovers = []struct{ name, what, how string }{
	{"worktrees", "browse copies `lore store open` made in an earlier 0.1.18 build (it now prints the store's clone); copy out anything you edited there", "rm -rf "},
	{"pii-patterns.txt.migrated", "secret patterns of an older lore, already moved into rows", "rm "},
}

// DoctorHomeLeftovers lists what earlier builds left in the lore home and
// the damaged cache copies kept for a week (notes: nothing is broken).
func (a *App) DoctorHomeLeftovers(context.Context) ([]DoctorFinding, error) {
	home := a.opts.Home
	var out []DoctorFinding
	for _, l := range homeLeftovers {
		p := filepath.Join(home, l.name)
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		out = append(out, DoctorFinding{Check: DoctorCheckHomeLeftovers, Level: DoctorNote,
			Message: p + " is " + l.what, Fix: l.how + p})
	}
	if n := len(corruptCopies(home)); n > 0 {
		out = append(out, DoctorFinding{Check: DoctorCheckHomeLeftovers, Level: DoctorNote,
			Message: strconv.Itoa(n) + " damaged cache file(s) moved aside under " + filepath.Join(home, cacheDir) + " (*" + corruptSuffix + "*); lore built fresh ones and removes these once they are a week old"})
	}
	return out, nil
}
