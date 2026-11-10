package agentdetect

import (
	"path"
	"strings"
)

// MaxAncestors is how far the walk goes up (section 3 step 4).
const MaxAncestors = 8

// Process is one ancestor as a process reader sees it.
type Process struct {
	PID int
	// Exe is the executable path or name.
	Exe string
	// Args are the command-line arguments after the executable.
	Args []string
}

// ProcessSource lists this process's ancestors, nearest first (the parent
// first), at most max. The per-OS readers live in adapters/procwalk; a
// reader that cannot walk returns what it has (or nil) and no error.
type ProcessSource interface {
	Ancestors(max int) []Process
}

// baseName is the lower-case base of an executable or script path, with
// .exe and script extensions removed ("/opt/bin/Codex.exe" → "codex").
func baseName(s string) string {
	s = strings.ReplaceAll(s, `\`, "/")
	b := strings.ToLower(path.Base(s))
	for _, ext := range []string{".exe", ".js", ".mjs", ".cjs", ".py", ".ts"} {
		b = strings.TrimSuffix(b, ext)
	}
	return b
}

// scriptOf is the first non-flag argument of a launcher ("" when none).
func scriptOf(args []string) string {
	for _, a := range args {
		if a != "" && !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// procMatches reports whether pr is one of d's processes, and the
// matched path (the script for a launcher, else the executable).
func procMatches(d Detector, pr Process) (string, bool) {
	exe := baseName(pr.Exe)
	for _, m := range d.Procs {
		if m.Script == "" {
			if exe == m.Exe {
				return pr.Exe, true
			}
			continue
		}
		if exe != m.Exe && !isLauncher(exe) {
			continue
		}
		if s := scriptOf(pr.Args); s != "" && baseName(s) == m.Script {
			return s, true
		}
	}
	return "", false
}

func isLauncher(exe string) bool {
	for _, l := range launchers {
		if exe == l {
			return true
		}
	}
	return false
}
