// Package gitremotetest is a forwarding shell: the code moved to github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest
// (PASS3 P3-PORT-LIVE). Every exported name here is an alias or a thin
// wrapper, so the old importers need no edit; new code imports the root
// package directly.
package gitremotetest

import (
	root "github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
	"testing"
)

// Clone forwards to the root package.
type Clone = root.Clone

// World forwards to the root package.
type World = root.World

// Constants forwarded from the root package.
const (
	DefaultBranch = root.DefaultBranch
	RemoteName    = root.RemoteName
)

// Variables forwarded from the root package.
var (
	Sig = root.Sig
)

// Git forwards to the root package.
func Git(t testing.TB, dir string, args ...string) string { return root.Git(t, dir, args...) }

// NewWorld forwards to the root package.
func NewWorld(t testing.TB) *root.World { return root.NewWorld(t) }
