package wire

// emptycontext.go — devco D-824 (hunt H-83, follow-up of D-821): `lore
// context --for-file/--for-prompt/--topic` with nothing applying printed
// "(nothing in the store applies here; try `lore search --query
// \"<keywords>\"`)", a try command with a placeholder that ran nowhere and
// no word of what was read. It now answers as an empty search does
// (emptysearch.go): under the empty line (contextop.NothingApplies) the
// stores and kinds read, how the file, prompt or topic was read, and 1-3
// next commands that run where printed (the same --store); --json carries
// the same as the top-level searched and try.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/op"
	contextop "github.com/thesatellite-ai/lore/ops/context"
)

// emptyReader is an output that says what it read when nothing applied
// (contextop.FetchOut).
type emptyReader interface {
	EmptyRead() (contextop.Reading, bool)
}

// noteEmptyContext returns res with what an empty row fetch read and what
// to try; res is returned as it is for any other context answer (the
// topic index, --internal, a hook, or an answer holding rows).
func (a *App) noteEmptyContext(ctx context.Context, storeName string, res op.Result) op.Result {
	v := res.Value()
	if !v.IsValid() || !v.CanInterface() {
		return res
	}
	// Interface() is the reflect boundary; it is narrowed at once.
	er, ok := v.Interface().(emptyReader)
	if !ok {
		return res
	}
	r, ok := er.EmptyRead()
	if !ok {
		return res
	}
	s := emptySearched{Stores: r.Stores, Kinds: r.Kinds, Words: r.Words, Path: r.Path, Folders: r.Folders, Topic: r.Topic}
	if storeName != "" {
		// a read naming its store ran on an App opened on that store
		s.Stores = a.namedStoreReads(ctx, storeName)
	}
	if s.Stores == nil {
		s.Stores = []string{}
	}
	if s.Kinds == nil {
		s.Kinds = []string{}
	}
	if s.Words == nil {
		s.Words = []string{}
	}
	return withEmptyNote(res, s, contextTries(r, storeName, a.contextPersonalUnread(ctx, storeName)))
}

// contextPersonalUnread reports whether a plain `lore search` from here
// would leave out the personal store or a folder of it (as an empty search
// asks, emptysearch.go): lore context never reads the personal store (its
// rows are the committed render's), so a note kept there is found only by
// a search naming --store me.
func (a *App) contextPersonalUnread(ctx context.Context, storeName string) bool {
	if storeName != "" || inStore(a.opts.RepoDir) {
		return false
	}
	var read []string
	for _, s := range a.readStores() {
		read = append(read, string(s))
	}
	return a.personalUnread(ctx, read)
}

// contextTries is the next commands of an empty context fetch, likeliest
// first, each with the fetch's --store so it runs where it is printed:
//
//	file    lore search on the path's words (every kind and store, word
//	        forms), lore rule list (a rule with no glob for the path), lore
//	        context --topics (topics whose globs may cover it)
//	prompt  lore search and lore ask on its words, lore rule list
//	topic   lore context --topics (the names there are), lore search on it
//
// personal (a plain search here would leave out the personal store or a
// folder of it) puts the same search with --store me first.
func contextTries(r contextop.Reading, storeName string, personal bool) []string {
	storeArg := ""
	if storeName != "" {
		storeArg = " --store " + storeName
	}
	var out []string
	words := strings.Join(r.Words, " ")
	if personal && words != "" && r.Topic == "" {
		out = append(out, "lore search --query "+shellArg(words)+" --store "+attach.ReservedMe)
	}
	switch {
	case r.Topic != "":
		out = append(out, "lore context --topics"+storeArg, "lore search --query "+shellArg(r.Topic)+storeArg)
	case r.Path != "":
		if words != "" {
			out = append(out, "lore search --query "+shellArg(words)+storeArg)
		}
		out = append(out, "lore rule list"+storeArg, "lore context --topics"+storeArg)
	default:
		if words != "" {
			out = append(out, "lore search --query "+shellArg(words)+storeArg, "lore ask --query "+shellArg(words)+storeArg)
		}
		out = append(out, "lore rule list"+storeArg)
	}
	if len(out) > emptyTryMax {
		out = out[:emptyTryMax]
	}
	return out
}
