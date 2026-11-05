// Package gitref is a forwarding shell: the code moved to github.com/thesatellite-ai/lore/adapters/gitref
// (PASS3 P3-PORT-LIVE). Every exported name here is an alias or a thin
// wrapper, so the old importers need no edit; new code imports the root
// package directly.
package gitref

import (
	"context"

	root "github.com/thesatellite-ai/lore/adapters/gitref"
)

// CASError forwards to the root package.
type CASError = root.CASError

// Changes forwards to the root package.
type Changes = root.Changes

// Commit forwards to the root package.
type Commit = root.Commit

// CommitRequest forwards to the root package.
type CommitRequest = root.CommitRequest

// Error forwards to the root package.
type Error = root.Error

// Exec forwards to the root package.
type Exec = root.Exec

// Git forwards to the root package.
type Git = root.Git

// History forwards to the root package.
type History = root.History

// Invocation forwards to the root package.
type Invocation = root.Invocation

// Repo forwards to the root package.
type Repo = root.Repo

// Signature forwards to the root package.
type Signature = root.Signature

// WriteRequest forwards to the root package.
type WriteRequest = root.WriteRequest

// WriteResult forwards to the root package.
type WriteResult = root.WriteResult

// Constants forwarded from the root package.
const (
	HistoryAppend  = root.HistoryAppend
	HistoryReplace = root.HistoryReplace
)

// Variables forwarded from the root package.
var (
	ErrBadParents       = root.ErrBadParents
	ErrBadPath          = root.ErrBadPath
	ErrBadRef           = root.ErrBadRef
	ErrCheckedOut       = root.ErrCheckedOut
	ErrNoGit            = root.ErrNoGit
	ErrNoSignature      = root.ErrNoSignature
	ErrNotFound         = root.ErrNotFound
	ErrNotRepo          = root.ErrNotRepo
	ErrPathClash        = root.ErrPathClash
	ErrRefMoved         = root.ErrRefMoved
	ErrUnsupportedEntry = root.ErrUnsupportedEntry
)

// ExitCode forwards to the root package.
func ExitCode(err error) (int, bool) { return root.ExitCode(err) }

// Open forwards to the root package.
func Open(ctx context.Context, dir string, g Git) (*Repo, error) { return root.Open(ctx, dir, g) }

// Stderr forwards to the root package.
func Stderr(err error) string { return root.Stderr(err) }
