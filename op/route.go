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

// RouteOf derives the REST route of an operation from its name and input
// fields (spec 9.3). ok is false for a Hidden operation (CLI only).
//
// The collection is the nouns before the verb, the last one pluralised
// (memory → memories) unless Spec.RESTNoun overrides it. An input field
// named "id" puts {id} in the path for show, edit, delete and custom verbs.
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
