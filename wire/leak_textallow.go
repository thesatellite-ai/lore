package wire

// leak_textallow.go — the text allows of the F16 text rule (#129): public
// text an internal note quotes verbatim (an approved product wording) may
// ship in a client repo when someone with write access to the note's store
// recorded an allow for that exact passage, with a reason.
//
//	internal stores indexed by leakRows ─▶ secret_allows rows with
//	rule_id internal-note (leakcheck.TextAllowRule) whose row_id is a note
//	of the SAME store ─▶ leakcheck.Options.TextAllows
//
// An allow in another store (the client repo, a client folder) is never
// read: only the note's own store can say its text is public.

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// secret_allows fields a text allow uses (schema.Builtin).
const (
	textAllowRowID  = "row_id"
	textAllowRule   = "rule_id"
	textAllowHash   = "match_hash"
	textAllowPath   = "path"
	textAllowReason = "reason"
)

// leakTextAllows reads the text allows of every store rows were indexed
// from; an allow counts only for a note of its own store. A store that
// cannot be read refuses ("cannot check"), as the index itself does.
func leakTextAllows(ctx context.Context, a leakReader, rows []leakcheck.IndexRow) ([]leakcheck.TextAllow, error) {
	notes := map[store.Source]map[string]bool{}
	for _, r := range rows {
		src := store.Source(r.Source)
		if src == "" || r.RepoKey {
			continue
		}
		if notes[src] == nil {
			notes[src] = map[string]bool{}
		}
		notes[src][r.ID] = true
	}
	var out []leakcheck.TextAllow
	for src, own := range notes {
		rs, err := a.List(ctx, src, schema.TableSecretAllows)
		if err != nil {
			return nil, leakCannotCheckErr("store "+string(src), err)
		}
		var names map[string]string
		for _, r := range rs {
			if r.Deleted || fieldText(r, textAllowRule) != leakcheck.TextAllowRule || !own[fieldText(r, textAllowRowID)] {
				continue
			}
			h, ok := textAllowHashOf(r)
			if !ok {
				continue
			}
			if names == nil {
				names = leakActorNames(ctx, a, src)
			}
			by := r.CreatedBy.String()
			if n := names[by]; n != "" {
				by = n
			}
			out = append(out, leakcheck.TextAllow{ID: r.ID.String(), RowID: fieldText(r, textAllowRowID), Hash: h,
				Path: fieldText(r, textAllowPath), Reason: fieldText(r, textAllowReason), By: by})
		}
	}
	return out, nil
}

// textAllowHashOf reads match_hash as the uint32 PassageHash.
func textAllowHashOf(r row.Row) (uint32, bool) {
	n, ok := r.Fields[textAllowHash].(value.Number)
	if !ok {
		return 0, false
	}
	h, err := strconv.ParseUint(strings.TrimSpace(n.String()), 10, 32)
	return uint32(h), err == nil
}

// leakActorNames maps the actor ids of src's roster to display names, so
// an allow names its author as a person, not an id; a roster that cannot
// be read leaves the ids.
func leakActorNames(ctx context.Context, a leakReader, src store.Source) map[string]string {
	out := map[string]string{}
	rs, err := a.List(ctx, src, schema.TableActors)
	if err != nil {
		return out
	}
	for _, r := range rs {
		if n := fieldText(r, schema.FieldActorDisplayName); n != "" {
			out[r.ID.String()] = n
		}
	}
	return out
}

// textAllowFix is the second way out of a text finding (#129): when the
// passage is approved public text the note quotes, the exact command that
// allows this passage of this note in this file, run where it is printed.
// A PR body or a live write has no repo file, so its allow names none.
func textAllowFix(f leakcheck.Finding, where string) string {
	return "if that passage is approved public text the note quotes: " + leakAllowCmd(f, where)
}

// metaAllowFix is the second way out of an exact-token or key-pair
// finding (#138): when the metadata value of that row is public, the
// command that allows it in this file; the finding's hash is the hash of
// the value's (or the pair's) words, so the allow covers that value only.
func metaAllowFix(f leakcheck.Finding, where string) string {
	return "if that metadata value of " + f.RowID + " is public: " + leakAllowCmd(f, where)
}

// leakAllowCmd is the `lore leaks allow` line for f: its row, the file
// when the action has one, its hash and a reason to fill in.
func leakAllowCmd(f leakcheck.Finding, where string) string {
	cmd := "lore leaks allow --note " + f.RowID
	if f.Path != "" && (where == LeakWhereCommit || where == LeakWherePush) {
		cmd += " --file " + pathArg(f.Path)
	}
	return cmd + " --match-hash " + strconv.FormatUint(uint64(f.Hash), 10) + " --reason \"<why it is public>\""
}

// textAllowedLines are the lines `lore check leaks` prints for findings a
// text allow covered: which allow let which passage through, and why.
func textAllowedLines(fs []leakcheck.Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		at := f.Path
		if at == "" {
			at = f.Where
		}
		if f.Line > 0 {
			at += ":" + strconv.Itoa(f.Line)
		}
		out = append(out, "lore: "+at+" "+f.Detail)
	}
	return out
}

// shellArgPlain is a word a POSIX shell takes as it is.
var shellArgPlain = regexp.MustCompile(`^[A-Za-z0-9._/@%+=:,-]+$`)

// pathArg is s as one shell word: as it is when plain, else quoted.
func pathArg(s string) string {
	if shellArgPlain.MatchString(s) {
		return s
	}
	return shellArg(s)
}
