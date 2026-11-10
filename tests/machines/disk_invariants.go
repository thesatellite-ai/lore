//go:build machines && !windows

package machines

// disk_invariants.go — what the files on disk must look like after lore
// ran on a machine: a tree snapshot taken before and after (path, mode,
// link target, content hash) shows every change, and each change must be
// in a place lore owns; config.yaml stays block style and keeps the keys
// the person wrote.

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// entry is one path of a snapshot.
type entry struct {
	Mode fs.FileMode
	Link string // symlink target, as written
	Sum  string // sha256 of a regular file
}

// tree maps an absolute path to its entry.
type tree map[string]entry

// Snapshot walks the whole machine folder (home, fakes, tmp) without
// following links.
func (m *Machine) Snapshot(t testing.TB) tree {
	t.Helper()
	out := tree{}
	_ = filepath.WalkDir(m.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // a folder lore made unreadable shows up as gone
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		e := entry{Mode: info.Mode()}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			e.Link, _ = os.Readlink(p)
		case info.Mode().IsRegular():
			if b, err := os.ReadFile(p); err == nil {
				s := sha256.Sum256(b)
				e.Sum = hex.EncodeToString(s[:])
			}
		}
		out[p] = e
		return nil
	})
	return out
}

// oldLoreLog is the fake old lore's own call log (env_two_lores), a
// fixture file written by the fake, not by lore; CheckOldLoreCalls reads
// what lore made it do.
const oldLoreLog = "old-lore-calls"

// CheckOldLoreCalls: lore only ever probes an older lore found on PATH
// (version, --help), never runs a real command with it.
func (m *Machine) CheckOldLoreCalls(t testing.TB, step string) {
	t.Helper()
	calls, _ := os.ReadFile(filepath.Join(m.Dir, oldLoreLog))
	for _, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		if line != "" && !strings.HasSuffix(line, " --help") && !strings.HasSuffix(line, " version") {
			t.Errorf("[%s] lore ran a command with the older lore on PATH (only probes are allowed): %q", step, line)
		}
	}
}

// skillDirs are the documented folders lore installs its skill bundle in
// (logic/toolprofile/skilldir.go), relative to the home folder.
var skillDirs = []string{".claude/skills/lore", ".codex/skills/lore"}

// lorePlaces are the folders lore may write in: its own ~/.lore, the
// machine's temp folder, every repo of the machine (rows, LORE.md,
// pointer files, git's own .git) and the skill folders. Anything else
// that changes is a write lore has no business making.
func (m *Machine) lorePlaces() []string {
	places := []string{filepath.Join(m.Home, ".lore"), filepath.Join(m.Dir, "tmp"), filepath.Join(m.Dir, oldLoreLog)}
	for _, d := range skillDirs {
		places = append(places, filepath.Join(m.Home, d))
	}
	for _, r := range m.Repos {
		places = append(places, r)
	}
	return places
}

// skillVersionLine starts the line of SKILL.md that marks a skill folder
// as lore's own (ops/skill stamps it on install).
const skillVersionLine = "lore_version:"

// PlacesBefore are lore's places on the machine as it is now (call it
// before the commands run): lorePlaces, the real paths of linked repos and
// linked ~/.lore files, plus the end of every skill-folder
// link (a chain followed) whose end is a lore skill folder — updating lore's
// own skill there is lore writing its own content. A link to anything else
// (a foreign folder, a dangling end) adds nothing: a write through it is a
// write lore has no business making.
func (m *Machine) PlacesBefore() []string {
	places := m.lorePlaces()
	// A repo reached through a linked folder (~/code → another disk) is
	// still the repo: its real path is a lore place too.
	for _, r := range m.Repos {
		if real, err := filepath.EvalSymlinks(r); err == nil && real != r {
			places = append(places, real)
		}
	}
	// A file of ~/.lore that is a link (a dotfiles repo owns config.yaml)
	// is lore's own file at the link's end: wire/configwrite.go writes it
	// there and keeps the link, so that one end file is a lore place.
	_ = filepath.WalkDir(filepath.Join(m.Home, ".lore"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		if end, eerr := filepath.EvalSymlinks(p); eerr == nil {
			if info, serr := os.Stat(end); serr == nil && info.Mode().IsRegular() {
				places = append(places, end)
			}
		}
		return nil
	})
	for _, d := range skillDirs {
		link := filepath.Join(m.Home, d)
		if info, err := os.Lstat(link); err != nil || info.Mode()&fs.ModeSymlink == 0 {
			continue
		}
		end, err := filepath.EvalSymlinks(link)
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(end, "SKILL.md"))
		if err != nil || !hasLine(string(b), skillVersionLine) {
			continue
		}
		places = append(places, end)
	}
	return places
}

func hasLine(body, prefix string) bool {
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			return true
		}
	}
	return false
}

func under(p string, roots []string) bool {
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// CheckChanges compares two snapshots: every added, removed or changed
// path must be inside a lore place, and a link the person made must still
// point where it did (lore never retargets or replaces someone's link).
func (m *Machine) CheckChanges(t testing.TB, step string, before, after tree, places []string) {
	t.Helper()
	report := func(p, what string) {
		if !under(p, places) {
			t.Errorf("[%s] %s outside lore's places: %s", step, what, rel(m.Home, p))
		}
	}
	for p, b := range before {
		a, ok := after[p]
		switch {
		case !ok:
			report(p, "removed")
		case b.Link != "" && a.Link != b.Link:
			t.Errorf("[%s] link %s changed: %q → %q", step, rel(m.Home, p), b.Link, a.Link)
		case a != b:
			report(p, "changed")
		}
	}
	for p, a := range after {
		if _, ok := before[p]; !ok && !(a.Mode.IsDir() && aboveAny(p, places)) {
			report(p, "added")
		}
	}
}

// aboveAny: p is a folder on the way to a lore place (~/.claude when lore
// makes ~/.claude/skills/lore).
func aboveAny(p string, places []string) bool {
	for _, r := range places {
		if strings.HasPrefix(r, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// CheckNoWriteThroughLinks: a file that changed under a lore place must
// not be reached through a symlink to a folder outside every lore place
// (a ~/.lore/x → ~/elsewhere link written through). Its real path is
// checked against the places' real paths.
func (m *Machine) CheckNoWriteThroughLinks(t testing.TB, step string, before, after tree, places []string) {
	t.Helper()
	var real []string
	for _, p := range places {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			real = append(real, r)
		}
	}
	for p, a := range after {
		if b, ok := before[p]; ok && a == b {
			continue
		}
		if a.Mode&fs.ModeSymlink != 0 || a.Mode.IsDir() {
			continue
		}
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			continue
		}
		if !under(r, real) {
			t.Errorf("[%s] %s was written through a link into %s, a folder lore does not own", step, rel(m.Home, p), r)
		}
	}
}

// flowLine is a YAML line whose value is a non-empty flow list or map.
var flowLine = regexp.MustCompile(`^\s*(- )?[^#\s][^:#]*:\s*[\[{]\s*[^\]}\s]`)

// configPath is ~/.lore/config.yaml.
func (m *Machine) configPath() string { return filepath.Join(m.Home, ".lore", "config.yaml") }

// CheckConfig asserts config.yaml (when there is one) parses, has no flow
// list or map the person did not write themselves (before holds the file
// as the shape built it, "" when there was none), and keeps every top-level
// key the person wrote.
func (m *Machine) CheckConfig(t testing.TB, step, before string) {
	t.Helper()
	b, err := os.ReadFile(m.configPath())
	if err != nil {
		if before != "" && !os.IsNotExist(err) {
			t.Errorf("[%s] config.yaml unreadable: %v", step, err)
		}
		return
	}
	var now map[string]any
	if err := yaml.Unmarshal(b, &now); err != nil {
		t.Errorf("[%s] config.yaml does not parse: %v\n%s", step, err, b)
		return
	}
	theirs := strings.Split(before, "\n")
	for i, line := range strings.Split(string(b), "\n") {
		if flowLine.MatchString(line) && !slices.Contains(theirs, line) {
			t.Errorf("[%s] config.yaml line %d is a flow list lore wrote: %q\n%s", step, i+1, line, b)
		}
	}
	var was map[string]any
	if yaml.Unmarshal([]byte(before), &was) == nil {
		for k := range was {
			if _, ok := now[k]; !ok && slices.Contains(personKeys(before), k) && !identityRetired(k, was, now) {
				t.Errorf("[%s] config.yaml lost the key %q the person wrote:\n%s", step, k, b)
			}
		}
	}
}

// identityRetired: key is the old top-level identity: and a config write
// moved it into a setup (SOURCE_OF_TRUTH row 1: the setup is the one
// source, the top-level block is retired on the next write), so a setup in
// now holds the same key. Any other loss is still a loss.
func identityRetired(key string, was, now map[string]any) bool {
	if key != "identity" {
		return false
	}
	top, _ := was[key].(map[string]any)
	want, _ := top["key"].(string)
	if want == "" {
		return false
	}
	setups, _ := now["setups"].([]any)
	for _, s := range setups {
		ent, _ := s.(map[string]any)
		id, _ := ent["identity"].(map[string]any)
		if got, _ := id["key"].(string); got == want {
			return true
		}
	}
	return false
}

// personKeys are the top-level keys of before that hold a value (an empty
// "identity:" a migration left is not the person's).
func personKeys(before string) []string {
	var was map[string]any
	if yaml.Unmarshal([]byte(before), &was) != nil {
		return nil
	}
	var keys []string
	for k, v := range was {
		if v != nil {
			keys = append(keys, k)
		}
	}
	return keys
}
