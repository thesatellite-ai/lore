package wire

// personalunread.go — a plain read names --store me when the personal
// store holds what it did not read (devco:D-771, hunt H-30). Notes written
// with `--to me/<folder>` live in folders of the personal store, and a read
// that names no store reads them nowhere: outside a repo it refused
// E_NOT_A_STORE with a hint naming only --store <org>, and in a repo it
// answered "(no matches)" with no word that the personal store's folders
// were not read, so an agent needed a second call (or gave up) where
// `--store me` answered at once.
//
//	plain read, refused outside a repo ─▶ hint leads with --store me
//	plain read, empty answer anywhere  ─▶ note: the personal store's
//	                                      folders (me/<folder>) were not
//	                                      read; add --store me
//
// Both only when a personal store is set up (and the command takes
// --store); the note only when that store has folders beside its top one,
// the part a plain read never attaches.

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
)

// PersonalUnreadField is the top-level --json field of a plain read whose
// empty answer left folders of the personal store unread: the same
// sentence the text note prints, so a --json caller gets the next step too.
const PersonalUnreadField = "personal_unread"

// personalUnreadMax is how many me/<folder> names the note lists before
// "…": enough to recognise the project, short enough for one line.
const personalUnreadMax = 3

// personalStoreFlag is the flag value that reads the whole personal store.
const personalStoreFlag = "--store " + attach.ReservedMe

// personalUnreadInfo declares PersonalUnreadField for the OpenAPI document.
var personalUnreadInfo = op.FieldInfo{
	Name: PersonalUnreadField,
	Type: op.TypeString,
	Desc: "on an empty answer of a read that named no store: the folders of your personal store it did not read and the --store me that reads them (absent otherwise)",
}

// topFieldsOf is the registry's top-level fields (op.Registry.WithTopFields)
// for entries: the resource route (D-623) on every routable read, and
// personalUnreadInfo on every row read taking --store, whose empty answer
// can carry it. A Spec does not list its input, so the --store readers are
// collected from the entries first.
func topFieldsOf(entries []op.Entry) func(op.Spec) []op.FieldInfo {
	withStore := map[string]bool{}
	for _, e := range entries {
		if takesStore(e) {
			withStore[e.Spec().Name] = true
		}
	}
	return func(spec op.Spec) []op.FieldInfo {
		out := routedTopFields(spec)
		if personalUnreadSpec(spec, withStore[spec.Name]) {
			out = append(out, personalUnreadInfo)
		}
		return out
	}
}

// personalUnreadSpec reports whether spec is a knowledge list or search
// (the nouns a repo with no lore project serves from the personal store,
// personalReadNouns), search or sql, taking --store (takesStore): the
// reads whose empty answer names --store me. A show names its row by id,
// which already finds it in any folder of the personal store (D-755).
func personalUnreadSpec(spec op.Spec, takesStore bool) bool {
	if !takesStore || !orgReadOp(spec) {
		return false
	}
	switch spec.Name {
	case opssearch.OpRun, opssql.OpRun:
		return true
	}
	noun, verb, ok := strings.Cut(spec.Name, ".")
	return ok && personalReadNouns[noun] && (verb == "list" || verb == "search")
}

// takesStore reports whether e takes --store (storeFlagsOf).
func takesStore(e op.Entry) bool {
	_, st, _ := storeFlagsOf(e)
	return st
}

// hasPersonalStore reports whether a personal store is set up for this
// folder (personal.store, or the active setup's personal_store; none of
// personal_store: none or v0.1.17's local store, whose reads are the
// repo's).
func (a *App) hasPersonalStore(ctx context.Context) bool {
	mc, err := readMachineConfig(a.opts.Home)
	return err == nil && effectivePersonalURL(ctx, a.opts.Home, a.projectDir(), mc) != ""
}

// personalStoreHint puts --store me first in the hint of err when err is
// an E_NOT_A_STORE refusal of a read taking --store that named no store
// (--store, --to, --project) and a personal store is set up: the person's
// own notes are the likeliest store they meant, and `--store me` reads
// them from any folder (personalRead), where the hint named only --store
// <org> (H-30). Any other err is returned as it is.
func (a *App) personalStoreHint(ctx context.Context, e op.Entry, d op.Decoder, err error) error {
	var le *errs.Error
	if err == nil || !errors.As(err, &le) || le.Code != errs.NotAStore || !takesStore(e) || !orgReadOp(e.Spec()) {
		return err
	}
	if strings.Contains(le.Hint, personalStoreFlag+" ") || !a.hasPersonalStore(ctx) {
		return err
	}
	in, derr := e.Decode(d)
	if derr != nil {
		return err
	}
	if n := scopeOf(in); n.to != "" || n.store != "" || n.project != "" {
		return err
	}
	hint := "add " + personalStoreFlag + " to read your personal store"
	if le.Hint != "" {
		hint += "; or " + le.Hint
	}
	return le.WithHint(hint)
}

// emptyLine matches the line an empty answer prints: v0.1.17's "(no
// matches)", "(no rules)", …, and sql's "(no rows)".
var emptyLine = regexp.MustCompile(`^\(no [a-z ]+\)$`)

// emptyAnswer reports whether text is an empty read's answer: its first
// line (lists, searches) or its last (sql, after the column header) is
// an emptyLine.
func emptyAnswer(text string) bool {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return emptyLine.MatchString(strings.TrimSpace(lines[0])) || emptyLine.MatchString(strings.TrimSpace(lines[len(lines)-1]))
}

// noteUnreadPersonal returns res with the personal-unread note when e is
// a plain read (no --store, --to or --project), its answer is empty, it
// ran outside a store's checkout (whose folder is what it reads), and the
// personal store has folders beside its top one. Any other res is
// returned as it is.
func (a *App) noteUnreadPersonal(ctx context.Context, e op.Entry, d op.Decoder, res op.Result) op.Result {
	if !personalUnreadSpec(e.Spec(), takesStore(e)) || inStore(a.opts.RepoDir) {
		return res
	}
	text, ok := res.Text()
	if !ok || !emptyAnswer(text) {
		return res
	}
	in, err := e.Decode(d)
	if err != nil {
		return res
	}
	if n := scopeOf(in); n.to != "" || n.store != "" || n.project != "" {
		return res
	}
	folders := a.personalUnreadFolders(ctx)
	if len(folders) == 0 {
		return res
	}
	note := personalUnreadNote(folders)
	return res.WithTextEdit(func(s string) string {
		return strings.TrimRight(s, "\n") + "\n  note: " + note + "\n"
	}).WithTopText(PersonalUnreadField, note)
}

// personalUnreadFolders is every me/<folder> of the personal store beside
// its top folder, the part no plain read attaches; nil with no personal
// store.
func (a *App) personalUnreadFolders(ctx context.Context) []string {
	if !a.hasPersonalStore(ctx) {
		return nil
	}
	me, found, _, err := a.baseStore(ctx, attach.ReservedMe)
	if err != nil || !found {
		return nil
	}
	var out []string
	for _, f := range a.personalFolders(ctx, me) {
		if f.Folder != "" && f.Folder != rowfs.FolderRoot {
			out = append(out, string(f.Name))
		}
	}
	return out
}

// personalUnreadNote is the note's sentence for the unread folders.
func personalUnreadNote(folders []string) string {
	shown := folders
	more := ""
	if len(shown) > personalUnreadMax {
		shown, more = shown[:personalUnreadMax], ", …"
	}
	return "your personal store's folders (" + strings.Join(shown, ", ") + more + ") were not read; add " + personalStoreFlag + " to the same command to read them"
}
