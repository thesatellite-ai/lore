package main

// home.go — a relative home folder is refused before anything opens
// (E-GAPS-1 S2). os.UserHomeDir hands $HOME back as it is, so HOME=../x
// made the lore home "../x/.lore" and the machine cache path a relative
// file: URI, which failed deep inside SQLite as "invalid uri authority:
// ..". Every later reader of the home (wire's Options.Home, the
// os.UserHomeDir calls in wire and wire/renderhost) runs after this check
// in the same process, so this one place covers them.

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/thesatellite-ai/lore/model/errs"
)

// homeEnvName is the variable os.UserHomeDir reads on this system.
func homeEnvName() string {
	switch runtime.GOOS {
	case "windows":
		return "USERPROFILE"
	case "plan9":
		return "home"
	}
	return "HOME"
}

// refuseRelativeHome answers E_INVALID_INPUT naming the home variable when
// it holds a relative path. An unset or unreadable home is not refused
// (dirs falls back to a temp lore home).
func refuseRelativeHome() error {
	uh, err := os.UserHomeDir()
	if err != nil || uh == "" || filepath.IsAbs(uh) {
		return nil
	}
	name := homeEnvName()
	return errs.New(errs.InvalidInput, name+" is the relative path "+strconv.Quote(uh)+"; it must be an absolute folder").
		WithHint("set " + name + " to an absolute path (for example the output of `cd \"$" + name + "\" && pwd`)")
}
