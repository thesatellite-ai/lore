package meta

// alias.go — meta.scope_aliases (METADATA 3.6, FA-META-12): a team's short
// names for scope labels, expanded in a grammar argument before it is
// parsed, so "be:status_code=500" reads as
// "repo:acmehub_backend:status_code=500".
//
//	ExpandAlias(arg, aliases)
//	  ─ key part = arg before its first operator
//	  ─ first ':' segment a level word, a type word, or key:type form? keep
//	  ─ first segment an alias? replace it with the alias's scope label

import "strings"

// AliasTargetOK reports whether target is a scope label an alias may name:
// a level word alone ("repo") or a level and a place ("repo:acmehub_backend").
func AliasTargetOK(target string) bool {
	lvl, place, hasPlace := strings.Cut(target, ":")
	if !IsLevel(lvl) {
		return false
	}
	return !hasPlace || (place != "" && !strings.Contains(place, ":"))
}

// AliasNameOK reports whether name may be an alias: a plain name that is
// neither a level word nor a type word (3.6; a key-name clash is checked
// where the registry is known).
func AliasNameOK(name string) bool {
	return name != "" && !strings.ContainsAny(name, ":=!~<>?. ") && !IsLevel(name) && !IsTypeWord(name)
}

// ExpandAlias rewrites arg's leading scope alias (see the file comment);
// an argument without one comes back unchanged. aliases maps alias →
// scope label; an entry that is not a valid alias or target is ignored.
func ExpandAlias(arg string, aliases map[string]string) string {
	if len(aliases) == 0 {
		return arg
	}
	end := len(arg)
	if i, _, err := findOp(arg); err == nil {
		end = i
	}
	parts := strings.Split(arg[:end], ":")
	if len(parts) < 2 || !AliasNameOK(parts[0]) {
		return arg
	}
	if len(parts) == 2 && IsTypeWord(parts[1]) {
		return arg // key:type, never an alias
	}
	target, ok := aliases[parts[0]]
	if !ok || !AliasTargetOK(target) {
		return arg
	}
	return target + arg[len(parts[0]):]
}

// ExpandAliases is ExpandAlias over every argument.
func ExpandAliases(args []string, aliases map[string]string) []string {
	if len(aliases) == 0 {
		return args
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = ExpandAlias(a, aliases)
	}
	return out
}
