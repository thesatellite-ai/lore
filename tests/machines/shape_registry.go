//go:build machines && !windows

package machines

// shape_registry.go — shapes beyond the six built into Build. Each
// shape_<area>.go file registers its shapes from an init func, so areas add
// shapes without editing machine.go. Build runs the common base (home,
// fakes, gitconfig, repos) and then the shape's Build func.

import "slices"

// ExtraShape is one registered shape.
type ExtraShape struct {
	// Name is the shape name Build accepts; must not clash.
	Name string
	// HomeName is the home folder's base name under the machine dir
	// ("" = "h"); a shape testing non-ASCII or spaced home paths sets it.
	HomeName string
	// Build finishes the machine after the common base: ssh, gh, ~/.lore
	// and whatever makes the shape what it is.
	Build func(b *builder)
}

var extraShapes = map[string]ExtraShape{}

// RegisterShape adds a shape; a duplicate or built-in name panics at init.
func RegisterShape(s ExtraShape) {
	if _, dup := extraShapes[s.Name]; dup || slices.Contains(AllShapes(), s.Name) || s.Build == nil {
		panic("machines: bad or duplicate shape " + s.Name)
	}
	extraShapes[s.Name] = s
}

func extraShapeNames() []string {
	var names []string
	for n := range extraShapes {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}
