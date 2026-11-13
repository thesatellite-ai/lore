// Package untouchedwalk is the shared part of S-2's walks, the standing
// rule "a repo with no lore data is never touched": a git repo with no
// .lore/ (and one only a store link names) stays byte-identical (its tree,
// .git/config, .git/hooks, .git/info, .git/lore) after every read and every
// command that is not init, setup or link.
//
// Two tests walk every command with it: app's TestUntouchedRepoEveryCommand
// (a plain repo, the whole command tree) and the journey
// TestJourneyNoLoreRepoUntouched (a repo a store folder links, every
// registry operation). Neither names the commands it runs: the list comes
// from the operation registry, and only the commands the walk may not run
// are named here, each with its reason (Skip).
package untouchedwalk

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/op"
)

// Skip are the commands the rule lets change the repo, or that a walk
// cannot run as a one-shot command; the key is a CLI path, matched as a
// prefix of whole words, so "setup" covers every setup subcommand (whose
// reads are still walked: ReadsOf).
var Skip = map[string]string{
	"init":             "sets the repo up: the one command that may create .lore/",
	"repo init":        "lore init's operation (lore repo init, the setup page's step)",
	"setup":            "setup's writes set repos up (its reads are still walked)",
	"onboard":          "the setup web page (a server that waits for a browser)",
	"org link":         "links the repo to an org store folder: a link is a setup step",
	"org unlink":       "undoes org link",
	"link add":         "links the repo to a store folder",
	"link remove":      "undoes link add",
	"spec":             "writes lore's own CLI and API reference files to the path it is given (default docs/reference/): a docs build tool whose output file is its job",
	"api":              "a server that runs until stopped",
	"tui":              "an interactive screen",
	"edit":             "opens an editor on a row; with no row it refuses before the repo",
	"open":             "opens a file in the person's editor or browser",
	"merge-driver":     "git's merge driver: git calls it with three temp files, never a person",
	"self-upgrade":     "downloads a release from the network",
	"completion":       "prints a shell completion script (cobra's own command)",
	"help":             "cobra's help",
	"__complete":       "cobra's completion protocol",
	"__completeNoDesc": "cobra's completion protocol",
}

// ReadsOf are the skipped groups whose reads must still leave the repo
// alone: a skip of the group covers only its writes.
var ReadsOf = map[string]bool{"setup": true}

// Skipped reports whether the CLI path is under a Skip key, and which.
func Skipped(path []string) (bool, string) {
	for i := len(path); i > 0; i-- {
		key := strings.Join(path[:i], " ")
		if _, ok := Skip[key]; ok {
			return true, key
		}
	}
	return false, ""
}

// Walked reports whether a walk runs the command at path; e is its
// registry entry, nil for a host command (init, api, …).
func Walked(path []string, e op.Entry) bool {
	skip, group := Skipped(path)
	if !skip {
		return true
	}
	return e != nil && ReadsOf[group] && e.Spec().OnlyReads()
}

// placeholder is the value every one-word input gets.
const placeholder = "walk"

// value is a placeholder for an input of type ft; ok is false for a type
// the CLI cannot take as one word.
func value(ft op.FieldType) (string, bool) {
	switch ft {
	case op.TypeInt, op.TypeUint, op.TypeFloat:
		return "1", true
	case op.TypeDuration:
		return "1h", true
	case op.TypeStringMap:
		return "k=v", true
	case op.TypeString, op.TypeStrings, op.TypeText:
		return placeholder, true
	}
	return "", false
}

// Args is the command line a walk runs for path: a registry operation (e
// not nil) gets every required input, positional or flag, and --yes when
// it can ask, so a write gets as far as it can; a host command runs bare.
func Args(path []string, e op.Entry) []string {
	args := slices.Clone(path)
	if e == nil {
		return args
	}
	var pos []string
	for _, f := range e.InputFields() {
		if !f.Required || f.Type == op.TypeBool {
			continue
		}
		v, ok := value(f.Type)
		if !ok {
			continue
		}
		switch {
		case f.Arg >= 0:
			for len(pos) <= f.Arg {
				pos = append(pos, placeholder)
			}
			pos[f.Arg] = v
		case f.Flag != "":
			args = append(args, "--"+f.Flag, v)
		}
	}
	args = append(args, pos...)
	if e.Spec().Confirm.CanAsk() {
		args = append(args, "--yes")
	}
	return args
}

// gitOwn are the parts of .git that git itself rewrites (objects, refs,
// logs, the index); the rest of .git (config, hooks, info, any folder lore
// could add) is part of a Tree.
var gitOwn = map[string]bool{".git/objects": true, ".git/refs": true, ".git/logs": true, ".git/index": true}

// Tree maps every folder and file under dir to its mode and content hash
// (gitOwn left out).
func Tree(dir string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, _ := filepath.Rel(dir, p)
		if gitOwn[filepath.ToSlash(rel)] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		h := fi.Mode().String()
		if fi.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			h += " " + hex.EncodeToString(sum[:])
		}
		out[rel] = h
		return nil
	})
	return out, err
}

// Diff lists what differs between two Trees, one line per path, "" for
// none.
func Diff(before, after map[string]string) string {
	var diff []string
	for p, h := range after {
		if b, ok := before[p]; !ok {
			diff = append(diff, "new: "+p)
		} else if b != h {
			diff = append(diff, "changed: "+p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			diff = append(diff, "gone: "+p)
		}
	}
	slices.Sort(diff)
	return strings.Join(diff, "\n")
}
