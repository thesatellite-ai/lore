//go:build stories && !windows

package stories

// WU_upwrite_test.go — story WW's printed-command checks from the owner's
// first use pass (devco D-538, D-539, D-543..D-549), on WW's world: writes
// from the home folder name their store with --to <org>/<folder> like reads
// name it with --store, and every next-step command a write or a refusal
// prints works exactly as printed, in the folder it was printed in.

import (
	"regexp"
	"strings"
	"testing"
)

// TestStoryWUPrintedCommandsWork runs the owner's use-pass steps and then
// the command each one printed, in the same folder.
func TestStoryWUPrintedCommandsWork(t *testing.T) {
	t.Parallel()
	w := wwBuild(t)
	m, home := w.m, w.m.Home
	app := w.places["own-linked"].Dir

	// D-538: a write from the home folder names its store with --to alone
	r := m.Lore(home, "memory", "add", "--body", "home write", "--to", wOrg+"/"+wFolder, "--json")
	t.Logf("memory add from home:\n%s", r)
	if r.Code != 0 {
		t.Errorf("devco:D-538: memory add --to %s/%s from the home folder refused:\n%s", wOrg, wFolder, r)
	}
	id := wwJSONID(r.Stdout)

	// D-539: a refusal's hint names only flags the command takes
	for _, args := range [][]string{
		{"context", "--for-prompt", "deploy"},
		{"memory", "edit", "--id", "mem_0000000000000000000000000000", "--body", "x"},
	} {
		r := m.Lore(home, args...)
		t.Logf("%v from home:\n%s", args, r)
		wuHintFlagsAccepted(t, w, "devco:D-539", args, r.Out())
	}

	// D-543: decision delete prints a restore line that works where printed
	dec := w.ids[wOrg]["decision"]
	r = m.Lore(app, "decision", "delete", "--id", dec, "--yes")
	t.Logf("decision delete in the linked repo:\n%s", r)
	wuRunPrinted(t, w, "devco:D-543", app, r.Out(), "restore:")

	// D-543 from the home folder: the row named by id, restored where printed
	r = m.Lore(home, "rule", "delete", "--id", w.ids[wOrg]["rule"], "--yes")
	t.Logf("rule delete from home:\n%s", r)
	wuRunPrinted(t, w, "devco:D-543", home, r.Out(), "restore:")

	// D-544: org config set prints a push command that works
	r = m.Lore(home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/wu-test/*", "--yes")
	t.Logf("org config set from home:\n%s", r)
	wuRunPrinted(t, w, "devco:D-544", home, r.Out(), "push it with:")

	// D-545: sql's hint names its query with --query
	r = m.Lore(w.places["store-root"].Dir, "sql", "--query", "SELECT nope FROM nothing")
	t.Logf("sql error:\n%s", r)
	if strings.Contains(r.Out(), `lore sql "`) {
		t.Errorf("devco:D-545: sql's hint names a positional query:\n%s", r.Out())
	}

	// D-546: repo link points at lore org link
	r = m.Lore(app, "repo", "link")
	t.Logf("repo link:\n%s", r)
	if !strings.Contains(r.Out(), "lore org link") {
		t.Errorf("devco:D-546: repo link does not point at lore org link:\n%s", r.Out())
	}

	// D-547: setup check with no args names the setups or the health check
	r = m.Lore(home, "setup", "check")
	t.Logf("setup check:\n%s", r)
	if !strings.Contains(r.Out(), wSetup) && !strings.Contains(r.Out(), "setup repair") {
		t.Errorf("devco:D-547: setup check with no arguments names neither the setups nor setup repair:\n%s", r.Out())
	}
	_ = id
}

// wuRunPrinted runs every `lore …` command after marker in out, in dir,
// and fails on a refusal of the kind a wrong hint gives.
func wuRunPrinted(t *testing.T, w *wwWorld, id, dir, out, marker string) {
	t.Helper()
	cmds := wuPrinted(out, marker)
	if len(cmds) == 0 {
		t.Errorf("%s: no %q command printed:\n%s", id, marker, out)
		return
	}
	for _, c := range cmds {
		r := w.m.Lore(dir, c...)
		t.Logf("printed command lore %s:\n%s", strings.Join(c, " "), r)
		if bad := wuBadRefusal(r.Code, r.Out()); bad != "" {
			t.Errorf("%s: the printed command `lore %s` fails where printed (%s):\n%s", id, strings.Join(c, " "), bad, r)
		}
	}
}

// wuMarkers are the places a command's output names a command to run next
// (the owner's GUARD: each must work exactly as printed).
var wuMarkers = []string{"restore:", "push it with:", "hint: lore ", "hint: `lore "}

// wuPrinted is every `lore …` command printed after marker in out, as
// arguments (without "lore"). A command with a placeholder (<id>, …) is
// a template, not a command, and is skipped.
func wuPrinted(out, marker string) [][]string {
	var cmds [][]string
	rest := out
	for {
		i := strings.Index(rest, marker)
		if i < 0 {
			return cmds
		}
		rest = rest[i+len(marker):]
		s := strings.TrimLeft(rest, " `")
		if strings.HasPrefix(marker, "hint: ") && strings.HasSuffix(marker, "lore ") {
			s = "lore " + s
		}
		if !strings.HasPrefix(s, "lore ") {
			continue
		}
		end := len(s)
		for _, stop := range []string{"`", ")", ";", "\n", " or ", ", or", ", then", " (", ", and"} {
			if j := strings.Index(s, stop); j >= 0 && j < end {
				end = j
			}
		}
		line := strings.TrimSpace(s[len("lore "):end])
		if line == "" || strings.ContainsAny(line, "<>…") {
			continue
		}
		cmds = append(cmds, wuCommand(wuSplit(line)))
	}
}

// wuProse are the words that start the prose after a command in a hint
// ("lore meta keys lists the keys", "lore store list shows the stores").
var wuProse = map[string]bool{"lists": true, "list's": true, "shows": true, "names": true, "prints": true, "sends": true,
	"takes": true, "works": true, "is": true, "are": true, "to": true, "then": true, "and": true, "first": true, "again": true, "now": true, "here": true}

// wuCommand is args cut at the first prose word outside a flag's value.
func wuCommand(args []string) []string {
	for i, a := range args {
		if !wuProse[a] {
			continue
		}
		if i > 0 && strings.HasPrefix(args[i-1], "--") && !wuBoolFlag(args[i-1]) {
			continue // a flag's value
		}
		return args[:i]
	}
	return args
}

// wuBoolFlag reports a printed flag that takes no value.
func wuBoolFlag(f string) bool {
	switch f {
	case "--yes", "--json", "--trash", "--push", "--all", "--create", "--force", "--local", "--dry-run":
		return true
	}
	return false
}

// wuSplit splits a printed command line into arguments, honouring single
// and double quotes.
func wuSplit(line string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	has := false
	for _, r := range line {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, has = r, true
		case r == ' ':
			if cur.Len() > 0 || has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 || has {
		out = append(out, cur.String())
	}
	return out
}

// wuBadRefusal names why a printed command's run is the kind of refusal a
// wrong hint gives ("" when it is not): a flag the command lacks, an input
// it refuses, or "this folder has no lore project".
func wuBadRefusal(code int, out string) string {
	if code == 0 {
		return ""
	}
	switch {
	case strings.Contains(out, "unknown flag") || strings.Contains(out, "unknown command"):
		return "unknown flag or command"
	case strings.Contains(out, "E_INVALID_INPUT"):
		return "E_INVALID_INPUT"
	case strings.Contains(out, "E_NOT_FOUND") && (strings.Contains(out, "no lore project") || strings.Contains(out, "lore init")):
		return "E_NOT_FOUND of the project kind"
	}
	return ""
}

// wuFlag is a --flag named in a hint.
var wuFlagRe = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

// wuHintFlagsAccepted fails when a refusal's hint names a flag the command
// (args up to the first flag) does not accept, by its --help.
func wuHintFlagsAccepted(t *testing.T, w *wwWorld, id string, args []string, out string) {
	t.Helper()
	i := strings.Index(out, "hint:")
	if i < 0 {
		return
	}
	hint := out[i:]
	var path []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			break
		}
		path = append(path, a)
	}
	help := w.m.Lore(w.m.Home, append(append([]string{}, path...), "--help")...).Out()
	for _, f := range wuFlagRe.FindAllString(hint, -1) {
		if f == "--help" || f == "--json" || strings.Contains(hint, f+" does not apply") {
			continue
		}
		// a flag of another command the hint names ("lore setup list --x")
		// is checked by running it, not here
		if j := strings.Index(hint, f); strings.Contains(hint[:j], "lore ") && !strings.Contains(hint[:j], "lore "+strings.Join(path, " ")) {
			continue
		}
		if !strings.Contains(help, f+" ") && !strings.Contains(help, f+"\n") && !strings.Contains(help, f+",") && !strings.Contains(help, f+"=") {
			t.Errorf("%s: lore %s refuses with a hint naming %s, which the command does not take:\n%s", id, strings.Join(path, " "), f, out)
		}
	}
}
