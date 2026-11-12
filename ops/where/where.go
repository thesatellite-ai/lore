// Package where defines `lore where` (alias `lore files`, layer L5; plain
// files sweep 3.2, 2026-11-12): every path lore owns on this machine and
// in the repo the command runs in, one line each with what it is, so
// nothing lore keeps is hidden. `--json` gives the same list.
//
//	where.show ─▶ Env.Where ─▶ the lore home walked (every entry named, an
//	   entry lore does not know listed as unknown), the store clones, the
//	   repo's files, git config entries, hook blocks and refs lore wrote,
//	   the skill folders ─▶ grouped lines / the entries as JSON
//
// It is Local: it reads files and git config only and opens no store, so
// it answers where a store is unreachable or the config is broken.
package where

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore where`, a bare-noun shortcut of show).
const (
	Noun   = "where"
	OpShow = "where.show"
	// ShowVerb is the generated verb the bare `lore where` runs.
	ShowVerb = "show"
	// Alias is the other spelling of the bare noun.
	Alias = "files"
)

// restNoun keeps the REST collection "where" un-pluralised.
const restNoun = "where"

// Areas of an entry.
const (
	// AreaHome is the lore home (~/.lore).
	AreaHome = "home"
	// AreaStore is a store clone on this machine.
	AreaStore = "store"
	// AreaRepo is the repo the command runs in (its files and its .git).
	AreaRepo = "repo"
	// AreaUser is outside both: the skill folders, the lore binary.
	AreaUser = "user"
)

// Kinds of an entry.
const (
	KindFile   = "file"
	KindFolder = "folder"
	// KindGitConfig is one git config key lore set (Path is the config
	// file, Key the key).
	KindGitConfig = "git-config"
	// KindGitRef is a ref (or a group of refs under one prefix) lore keeps.
	KindGitRef = "git-ref"
	// KindBlock is lore's marked block inside a file that is not lore's
	// (.gitignore, a hook, CLAUDE.md).
	KindBlock = "block"
)

// ShowIn is the input of where.show.
type ShowIn struct {
	All bool `json:"all,omitempty" flag:"all" desc:"also list the places lore uses that do not exist on this machine yet"`
}

// Entry is one path lore owns.
type Entry struct {
	// Area is home, store, repo or user.
	Area string `json:"area"`
	// Path is the absolute path (for a git config key, the config file).
	Path string `json:"path"`
	// Key is the git config key or ref prefix (KindGitConfig, KindGitRef).
	Key string `json:"key,omitempty"`
	// Kind is file, folder, git-config, git-ref or block.
	Kind string `json:"kind"`
	// What says what it is, in plain words.
	What string `json:"what"`
	// Store is the short name of the store a store-area entry belongs to
	// (the org's name for the store holding the org folder; the url when
	// no configured org lists it).
	Store string `json:"store,omitempty"`
	// ByHand says what happens when a person edits or deletes it.
	ByHand string `json:"by_hand,omitempty"`
	// Exists is false only with --all (a place lore would use).
	Exists bool `json:"exists"`
	// Collapsed marks a folder listed as one entry for everything in it
	// (a store clone's files, a repo's rows, a folder of logs).
	Collapsed bool `json:"collapsed,omitempty"`
}

// Out is the output of where.show.
type Out struct {
	// Home is the lore home.
	Home string `json:"home"`
	// Readme is the lore home's README.md, the map of what each entry is
	// ("" until lore wrote it).
	Readme string `json:"readme,omitempty"`
	// Repo is the repo the command ran in ("" outside one).
	Repo string `json:"repo,omitempty"`
	// Entries are every path lore owns, by area.
	Entries []Entry `json:"entries"`
	// Unknown are paths in the lore home lore does not recognise (made by
	// hand, or by another program): never deleted, only named.
	Unknown []string `json:"unknown,omitempty"`
}

// Env is what where needs; *wire.App implements it.
type Env interface {
	Where(ctx context.Context, in ShowIn) (Out, error)
}

// areaTitles are the section headers of the human form, in order.
var areaTitles = []struct{ area, title string }{
	{AreaHome, "lore's home"},
	{AreaStore, "store clones"},
	{AreaRepo, "this repo"},
	{AreaUser, "elsewhere for you"},
}

// Text implements op.Texter: one section per area, one line per entry.
func (o Out) Text() string {
	var b strings.Builder
	for _, a := range areaTitles {
		var lines []Entry
		for _, e := range o.Entries {
			if e.Area == a.area {
				lines = append(lines, e)
			}
		}
		if len(lines) == 0 {
			continue
		}
		switch a.area {
		case AreaHome:
			fmt.Fprintf(&b, "%s: %s\n", a.title, o.Home)
		case AreaRepo:
			fmt.Fprintf(&b, "%s: %s\n", a.title, o.Repo)
		default:
			fmt.Fprintf(&b, "%s:\n", a.title)
		}
		base := ""
		switch a.area {
		case AreaHome:
			base = o.Home
		case AreaRepo:
			base = o.Repo
		}
		for _, e := range lines {
			p := relTo(base, e.Path)
			if e.Key != "" {
				p += " [" + e.Key + "]"
			}
			if e.Store != "" {
				p += " (store " + e.Store + ")"
			}
			if !e.Exists {
				p += " (not there yet)"
			}
			fmt.Fprintf(&b, "  %s\n      %s\n", p, e.What)
		}
		b.WriteString("\n")
	}
	if len(o.Unknown) > 0 {
		b.WriteString("in lore's home but not lore's (left as they are):\n")
		for _, u := range o.Unknown {
			fmt.Fprintf(&b, "  %s\n", u)
		}
		b.WriteString("\n")
	}
	if o.Readme != "" {
		b.WriteString("what each one is and what happens if you edit or delete it: " + o.Readme + "; `lore where --json` lists the same\n")
	}
	return b.String()
}

// relTo is p relative to base when it lies inside it (the section header
// names base), else p as it is.
func relTo(base, p string) string {
	if base == "" {
		return p
	}
	rel, err := filepath.Rel(base, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return p
	}
	return rel
}

// Ops returns the where operation bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpShow, Summary: "List every file, folder, git config entry and ref lore keeps on this machine and in this repo, with what each one is (alias: lore files)",
			Permission: op.PermRead, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			Errors: []errs.Code{errs.Internal},
			Examples: []op.Example{
				{Title: "everything lore keeps here", CLI: "lore where", Want: "lore's home"},
				{Title: "the same as JSON", CLI: "lore where --json", Want: "entries"},
			},
		}, func(ctx context.Context, in ShowIn) (Out, error) {
			return env.Where(ctx, in)
		}),
	}
}
