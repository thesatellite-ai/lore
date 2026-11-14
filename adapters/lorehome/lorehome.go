// Package lorehome finds the lore home: the one folder that holds every
// piece of lore's machine state (config.yaml, identity, stores/, cache,
// state, logs, locks, render output, machine.db, README.md).
//
//	LORE_HOME set ──▶ that absolute folder (relative, ~ or empty: refused)
//	unset ─────────▶ <user home>/.lore
//	no user home ──▶ <temp>/lore-home-unresolved
//
// Why one resolver: two lore builds on one machine (a released lore and a
// preview run as another name) shared ~/.lore, so the newer one rewrote
// the older one's config.yaml and store clones. LORE_HOME gives each build
// its own home; HOME itself is never changed, so git, ssh and gpg keep
// their own files. Every reader of the lore home takes it from Resolve
// (app passes it on as wire.Options.Home); os.UserHomeDir elsewhere means
// the person's home, for ~ in paths and the tools' own folders.
package lorehome

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

const (
	// EnvName is the variable that moves the lore home.
	EnvName = "LORE_HOME"
	// DirName is the lore home's folder under the user's home by default.
	DirName = ".lore"
	// UnresolvedDir is the folder, under the temp folder, used when the
	// user's home cannot be read and LORE_HOME is unset.
	UnresolvedDir = "lore-home-unresolved"
)

// Source says where the lore home came from (`lore where`, doctor).
type Source string

const (
	// FromEnv is a home set by LORE_HOME.
	FromEnv Source = "LORE_HOME"
	// FromDefault is <user home>/.lore.
	FromDefault Source = "default"
	// FromFallback is the temp folder used when no user home is known.
	FromFallback Source = "fallback"
)

// Home is the resolved lore home and where it came from.
type Home struct {
	// Dir is the absolute, cleaned folder (never resolved through links:
	// a symlinked home stays the path the person set).
	Dir string
	// Source is LORE_HOME, default or fallback.
	Source Source
}

// Env is what Resolve reads; System reads the process.
type Env struct {
	// Lookup reads a variable (os.LookupEnv).
	Lookup func(string) (string, bool)
	// UserHome is the person's home (os.UserHomeDir).
	UserHome func() (string, error)
	// TempDir is the temp folder (os.TempDir).
	TempDir func() string
}

// System is the process's own environment.
func System() Env {
	return Env{Lookup: os.LookupEnv, UserHome: os.UserHomeDir, TempDir: os.TempDir}
}

// Resolve is the lore home of this process.
func Resolve() (Home, error) { return System().Resolve() }

// Resolve is the lore home e names. A LORE_HOME that is set but empty,
// relative, or starts with ~ (a shell did not expand it, and lore does not
// guess) is E_INVALID_INPUT naming the value and the fix.
func (e Env) Resolve() (Home, error) {
	if v, ok := e.Lookup(EnvName); ok {
		if err := check(v); err != nil {
			return Home{}, err
		}
		return Home{Dir: filepath.Clean(v), Source: FromEnv}, nil
	}
	uh, err := e.UserHome()
	if err != nil || uh == "" {
		return Home{Dir: filepath.Join(e.TempDir(), UnresolvedDir), Source: FromFallback}, nil
	}
	return Home{Dir: filepath.Join(uh, DirName), Source: FromDefault}, nil
}

// check refuses a LORE_HOME value lore cannot use as it is.
func check(v string) error {
	switch {
	case strings.TrimSpace(v) == "":
		return errs.New(errs.InvalidInput, EnvName+" is set but empty; it must be an absolute folder").
			WithHint("unset " + EnvName + " to use ~/" + DirName + ", or set it to an absolute path (for example " + EnvName + "=\"$HOME/.lore-dev\")")
	case strings.HasPrefix(v, "~"):
		return errs.New(errs.InvalidInput, EnvName+" is "+strconv.Quote(v)+": lore does not expand ~; it must be an absolute folder").
			WithHint("write the home out (for example " + EnvName + "=\"$HOME/" + strings.TrimPrefix(strings.TrimPrefix(v, "~"), "/") + "\"), which the shell expands")
	case !filepath.IsAbs(v):
		return errs.New(errs.InvalidInput, EnvName+" is the relative path "+strconv.Quote(v)+"; it must be an absolute folder").
			WithHint("set " + EnvName + " to an absolute path (for example the output of `cd \"$" + EnvName + "\" && pwd`)")
	}
	return nil
}
