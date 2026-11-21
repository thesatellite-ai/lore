package cli

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/op"
)

// Narrowing the command tree (devco:D-826). Building a command for every
// operation (flags, usage, help text, and the host's wrappers on each)
// cost every lore process ~10 ms, and reading every operation's input
// fields for it another ~13 ms, for the one command it runs. When the
// words of a call name one noun's commands without any guessing, only
// that noun's operations are mounted; every other call (help, completion,
// a word no operation path has, a host command, a flag first) gets the
// full tree, so what cobra resolves, prints or suggests is the same.

// helpWords are the arguments that print help: help lists sibling and
// parent commands, so it always gets the full tree.
var helpWords = []string{"--help", "-h"}

// NarrowNouns returns the first words of the operations a call with args
// needs mounted, or nil when it needs the full tree. It narrows only when
// args[0] is an operation noun and each following word, up to the first
// flag, either continues an operation's path or is a positional of the
// leaf operation reached so far. A word under a group that no operation
// path has (a default verb's positional, a host sub-command, a typo whose
// "did you mean" walks the whole tree) gets the full tree.
func NarrowNouns(reg *op.Registry, args []string) []string {
	if reg == nil || len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil
	}
	for _, a := range args {
		if slices.Contains(helpWords, a) || strings.HasPrefix(a, "--help=") {
			return nil
		}
	}
	var paths [][]string
	for _, e := range reg.Entries() {
		if p := op.CLIPath(e.Spec().Name); len(p) > 0 && p[0] == args[0] {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	prefix := []string{args[0]}
	for _, w := range args[1:] {
		if strings.HasPrefix(w, "-") {
			break
		}
		if pathStarts(paths, append(slices.Clone(prefix), w)) {
			prefix = append(prefix, w)
			continue
		}
		if !isPath(paths, prefix) {
			return nil
		}
		break // a positional of the leaf operation
	}
	return []string{args[0]}
}

// pathStarts reports whether some path begins with prefix.
func pathStarts(paths [][]string, prefix []string) bool {
	for _, p := range paths {
		if len(p) >= len(prefix) && slices.Equal(p[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// isPath reports whether prefix is a whole operation path (a leaf).
func isPath(paths [][]string, prefix []string) bool {
	for _, p := range paths {
		if slices.Equal(p, prefix) {
			return true
		}
	}
	return false
}

// WithNouns mounts only the operations whose command path starts with
// one of nouns (NarrowNouns); nil or empty mounts every operation.
func WithNouns(nouns []string) Option {
	return func(c *config) { c.nouns = nouns }
}

// mounts reports whether Mount adds e under cfg's nouns.
func (cfg config) mounts(e op.Entry) bool {
	if len(cfg.nouns) == 0 {
		return true
	}
	p := op.CLIPath(e.Spec().Name)
	return len(p) > 0 && slices.Contains(cfg.nouns, p[0])
}
