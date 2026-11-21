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

// ReadCall returns the operation a call with args runs when that is one
// operation that only reads (op.Spec.OnlyReads), resolved exactly as
// cobra will: args[0] an operation noun, each following word up to the
// first flag continuing an operation's path, then the leaf's positionals.
// defaults maps a group's words (joined by a space) to the verb its bare
// spelling runs (`lore ask` runs ask run): a call that stops at such a
// group resolves to that verb, as the host's default-verb commands
// forward it. ok is false for anything else (a write, a host command, help, a flag
// first, a shortcut, a group word): the read server (app/readserver,
// devco:D-833) serves only calls this accepts and leaves the rest to a
// one-shot process.
func ReadCall(reg *op.Registry, args []string, defaults map[string]string) (op.Spec, bool) {
	if reg == nil || len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return op.Spec{}, false
	}
	for _, a := range args {
		if slices.Contains(helpWords, a) || strings.HasPrefix(a, "--help=") {
			return op.Spec{}, false
		}
	}
	byPath := map[string]op.Spec{}
	var paths [][]string
	for _, e := range reg.Entries() {
		if p := op.CLIPath(e.Spec().Name); len(p) > 0 && p[0] == args[0] {
			paths = append(paths, p)
			byPath[strings.Join(p, " ")] = e.Spec()
		}
	}
	prefix := []string{args[0]}
	for _, w := range args[1:] {
		if strings.HasPrefix(w, "-") || !pathStarts(paths, append(slices.Clone(prefix), w)) {
			break
		}
		prefix = append(prefix, w)
	}
	at := strings.Join(prefix, " ")
	s, ok := byPath[at]
	if verb, isGroup := defaults[at]; !ok && isGroup {
		s, ok = byPath[at+" "+verb]
	}
	if !ok || !s.OnlyReads() {
		return op.Spec{}, false
	}
	return s, true
}
