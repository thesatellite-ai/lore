// Package mergewatch is a forwarding shell: the code moved to
// github.com/thesatellite-ai/lore/adapters/mergewatch (PASS3 p3-mergewatch),
// tests included. Every exported name here is an alias, so the old
// importers need no edit; new code imports the root package directly.
package mergewatch

import (
	"context"

	root "github.com/thesatellite-ai/lore/adapters/mergewatch"
)

// TrailerKey forwards to the root package.
const TrailerKey = root.TrailerKey

// Method forwards to the root package.
type Method = root.Method

// Method values forward to the root package.
const (
	MethodAncestry = root.MethodAncestry
	MethodTrailer  = root.MethodTrailer
	MethodAdapter  = root.MethodAdapter
	MethodManual   = root.MethodManual
)

// Candidate forwards to the root package.
type Candidate = root.Candidate

// Finding forwards to the root package.
type Finding = root.Finding

// Step forwards to the root package.
type Step = root.Step

// Adapter forwards to the root package.
type Adapter = root.Adapter

// Checker forwards to the root package.
type Checker = root.Checker

// ErrNoTarget forwards to the root package (the same sentinel value).
var ErrNoTarget = root.ErrNoTarget

// AddTrailer forwards to the root package.
func AddTrailer(msg []byte, id string) []byte { return root.AddTrailer(msg, id) }

// Runner forwards to the root package.
type Runner = root.Runner

// ExecRunner forwards to the root package.
func ExecRunner(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	return root.ExecRunner(ctx, dir, name, args...)
}

// GitHub forwards to the root package.
type GitHub = root.GitHub
