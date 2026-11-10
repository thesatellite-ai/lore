package op

import (
	"net/http"
	"strings"
)

// Verbs with a fixed REST mapping (spec 9.3). Any other verb is a POST.
const (
	VerbAdd    = "add"
	VerbList   = "list"
	VerbShow   = "show"
	VerbEdit   = "edit"
	VerbDelete = "delete"
)

// RESTPrefix is the version prefix of every route.
const RESTPrefix = "/v1"

// idField is the input field that becomes the {id} path parameter.
const idField = "id"

// idParam is the path parameter placeholder.
const idParam = "{id}"

// Route is one REST route.
type Route struct {
	Method string
	Path   string
	// Status is the success status: 201 for add, else 200.
	Status int
}

// CLIPath returns the CLI words of an operation: "secrets.rule.add" →
// [secrets rule add]; "repo.set_url" → [repo set-url].
func CLIPath(name string) []string {
	p, ok := parts(name)
	if !ok {
		return nil
	}
	out := make([]string, len(p))
	for i, s := range p {
		out[i] = strings.ReplaceAll(s, "_", "-")
	}
	return out
}

// PathIDField is the input field the {id} path parameter of a show, edit
// or delete route fills: the field named "id", else the first positional
// argument (setup.show's name, task.template.show's name: what the CLI
// takes as `lore setup show <name>`). ok is false when the operation has
// neither, and then its route has no {id} (RouteOf): a path id it could not take
// would only make the decoder refuse every call (whoami.show,
// identity.show answered E_INVALID_INPUT to every GET until this rule).
// A variadic (rest) argument is a list, never one path segment.
func PathIDField(in []FieldInfo) (string, bool) {
	for _, f := range in {
		if f.Name == idField {
			return f.Name, true
		}
	}
	for _, f := range in {
		if f.Arg == 0 && !f.Rest {
			return f.Name, true
		}
	}
	return "", false
}

// RouteOf derives the REST route of an operation from its name and input
// fields (spec 9.3). ok is false for a Hidden operation (CLI only).
//
// The collection is the nouns before the verb, the last one pluralised
// (memory → memories) unless Spec.RESTNoun overrides it. show, edit and
// delete put {id} in the path when the operation can take it (PathIDField:
// an "id" field, else its first positional argument), else the verb
// itself (GET /v1/whoami/show); a custom verb puts {id} in the path only for
// an input field named "id".
func RouteOf(s Spec, in []FieldInfo) (Route, bool) {
	p, valid := parts(s.Name)
	if !valid || s.Hidden {
		return Route{}, false
	}
	verb := p[len(p)-1]
	nouns := append([]string(nil), p[:len(p)-1]...)
	last := len(nouns) - 1
	if s.RESTNoun != "" {
		nouns[last] = s.RESTNoun
	} else {
		nouns[last] = Plural(nouns[last])
	}
	base := RESTPrefix + "/" + strings.Join(nouns, "/")
	hasID := false
	for _, f := range in {
		if f.Name == idField {
			hasID = true
		}
	}
	withID := base + "/" + idParam
	if _, ok := PathIDField(in); !ok && (verb == VerbShow || verb == VerbEdit || verb == VerbDelete) {
		// The verb names the path, as for a custom verb: the bare
		// collection path is the list (GET) or the bulk twin (PATCH,
		// DELETE) of the noun (store.show next to store.list).
		withID = base + "/" + verb
	}
	switch verb {
	case VerbAdd:
		return Route{Method: http.MethodPost, Path: base, Status: http.StatusCreated}, true
	case VerbList:
		return Route{Method: http.MethodGet, Path: base, Status: http.StatusOK}, true
	case VerbShow:
		return Route{Method: http.MethodGet, Path: withID, Status: http.StatusOK}, true
	case VerbEdit:
		return Route{Method: http.MethodPatch, Path: withID, Status: http.StatusOK}, true
	case VerbDelete:
		return Route{Method: http.MethodDelete, Path: withID, Status: http.StatusOK}, true
	}
	if hasID {
		return Route{Method: http.MethodPost, Path: withID + "/" + verb, Status: http.StatusOK}, true
	}
	return Route{Method: http.MethodPost, Path: base + "/" + verb, Status: http.StatusOK}, true
}

// Plural is the default English plural of a snake_case noun: consonant+y →
// ies; ss/x/z/ch/sh → es; a noun already ending in s is kept (lore names
// some nouns in the plural: "secrets", "vars"); else s. Irregular nouns use
// Spec.RESTNoun.
func Plural(noun string) string {
	switch {
	case noun == "":
		return noun
	case strings.HasSuffix(noun, "y") && len(noun) > 1 && !strings.ContainsRune("aeiou", rune(noun[len(noun)-2])):
		return noun[:len(noun)-1] + "ies"
	case strings.HasSuffix(noun, "ss"), strings.HasSuffix(noun, "x"), strings.HasSuffix(noun, "z"),
		strings.HasSuffix(noun, "ch"), strings.HasSuffix(noun, "sh"):
		return noun + "es"
	case strings.HasSuffix(noun, "s"):
		return noun
	}
	return noun + "s"
}
