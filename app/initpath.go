package app

// initpath.go — `lore init [--path <path>]` (CLI_REFERENCE 2.14, v0.1.17's
// `init [path]`): the app is built for one checkout before the command
// runs, so the path is taken off the arguments first and becomes the
// working directory of the whole run (repo, work dir and .gitignore).
//
//	run(args) ──▶ initPathArg(args) ──▶ "<path>" ──▶ enterInitDir: chdir
//	              into it, else E_BAD_PATH ──▶ dirs() reads it back as wd

import (
	"os"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// initCmdName is the command whose positional is a path.
const initCmdName = "init"

// initValueFlags are init's flags that take a value in the next argument
// (`--name x`, `--target codex`); the value is never the path.
var initValueFlags = map[string]bool{"name": true, "target": true}

// initPathFlag names the folder by flag, `lore init --path <path>` (U-4:
// the canonical form; the positional path stays a quiet alias).
const initPathFlag = "path"

// initPathArg returns the path of `lore init --path <path>` (or the
// positional `lore init --path <path>`) and args without it; ok is false for any
// other command or a bare `lore init`.
func initPathArg(args []string) (path string, rest []string, ok bool) {
	if len(args) == 0 || args[0] != initCmdName {
		return "", args, false
	}
	for i := 1; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if i+1 < len(args) {
				return args[i+1], append(cloneArgs(args[:i]), args[i+2:]...), true
			}
			return "", args, false
		}
		if v, cut := strings.CutPrefix(a, "--"+initPathFlag+"="); cut {
			return v, append(cloneArgs(args[:i]), args[i+1:]...), true
		}
		if a == "--"+initPathFlag && i+1 < len(args) {
			return args[i+1], append(cloneArgs(args[:i]), args[i+2:]...), true
		}
		if strings.HasPrefix(a, "-") {
			if name := strings.TrimLeft(a, "-"); initValueFlags[name] && !strings.Contains(a, "=") {
				i++ // the flag's value
			}
			continue
		}
		return a, append(cloneArgs(args[:i]), args[i+1:]...), true
	}
	return "", args, false
}

// cloneArgs copies s, so appending to the copy never writes into args.
func cloneArgs(s []string) []string { return append([]string(nil), s...) }

// enterInitDir makes the path of `lore init --path <path>` the process's working
// directory (run then reads it back with dirs, as for a plain `lore
// init`). A missing path or a file is E_BAD_PATH: init sets up an
// existing checkout, it never creates one.
func enterInitDir(path string) error {
	if err := os.Chdir(path); err != nil {
		return errs.Wrap(errs.BadPath, "init: "+path+" is not a folder", err).WithHint("create it first, or run lore init inside it")
	}
	return nil
}
