package settings

// leaks_allow.go — `lore leaks allow` (#129): an allow for a text-rule
// finding, for public text (approved product wording) that an internal
// note quotes verbatim. The entry is a secret_allows row with rule_id
// internal-note (leakcheck.TextAllowRule), written in the note's OWN store
// (never the client repo): row_id the note, match_hash the PassageHash of
// the exact passage, path the one repo file when narrowed, reason required,
// its author the row's created_by. `lore secrets allow list` lists it,
// `lore secrets allow rm` removes it, `lore check leaks` names it when it
// lets a passage through, and `lore doctor` lists every one.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/schema"
)

// OpLeaksAllow is `lore leaks allow`.
const OpLeaksAllow = "leaks.allow"

// LeaksAllowIn is the input of leaks.allow.
type LeaksAllowIn struct {
	Note string `json:"note" flag:"note" desc:"the internal note the passage is quoted from, or the row whose metadata value it is (the id the refusal names)"`
	// Phrase or MatchHash names the one passage: the exact text, or the
	// hash a refused commit prints. Only the hash is stored.
	Phrase    string `json:"phrase,omitempty" flag:"phrase" desc:"the exact public passage (only its hash is stored)"`
	MatchHash string `json:"match_hash,omitempty" flag:"match-hash" desc:"the passage hash a refused commit or push prints, instead of --phrase"`
	File      string `json:"file,omitempty" flag:"file" desc:"allow the passage only in this repo file (default: anywhere)"`
	Reason    string `json:"reason" flag:"reason" desc:"why the passage is public (required; lore doctor and lore check leaks show it)"`
}

func (o ops) leaksOps() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: OpLeaksAllow, Summary: "Allow one public passage an internal note quotes verbatim, or one public metadata value of a row (a text, exact-token or key-pair finding)", Permission: op.PermWrite,
			Access: op.AccessWrite, Errors: writeErrors, Examples: []op.Example{{Title: "ship approved banner wording a decision quotes",
				CLI: `lore leaks allow --note dec_0c3a --file copy.txt --phrase "Your order has been received and a support agent will contact you soon." --reason "approved public copy"`}}}, o.leaksAllow),
	}
}

// leaksAllow writes one text allow in the note's own store.
func (o ops) leaksAllow(ctx context.Context, in LeaksAllowIn) (RowOut, error) {
	if strings.TrimSpace(in.Reason) == "" {
		return RowOut{}, errs.New(errs.InvalidInput, "leaks allow: --reason is required (why the passage is public)")
	}
	phrase, hashText := strings.TrimSpace(in.Phrase), strings.TrimSpace(in.MatchHash)
	if (phrase == "") == (hashText == "") {
		return RowOut{}, errs.New(errs.InvalidInput, "leaks allow: give --phrase <the passage> or --match-hash <n>, one of them").
			WithHint("the refused commit prints the exact lore leaks allow line with its --match-hash")
	}
	noteID, err := ids.ParseAny(strings.TrimSpace(in.Note))
	if err != nil {
		return RowOut{}, lookup.BadID("leaks allow: "+quote(in.Note)+" is not a row id (--note takes the id the refusal names)", err)
	}
	var hash uint32
	if phrase != "" {
		words := leakcheck.Words(phrase)
		if len(words) < leakcheck.MinRunWords {
			return RowOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("leaks allow: the passage has %d words; the check only blocks %d or more in a row, so it needs no allow", len(words), leakcheck.MinRunWords))
		}
		hash = leakcheck.PassageHash(words)
	} else {
		n, perr := strconv.ParseUint(hashText, 10, 32)
		if perr != nil {
			return RowOut{}, errs.New(errs.InvalidInput, "leaks allow: --match-hash "+quote(hashText)+" is not the number a refusal prints")
		}
		hash = uint32(n)
	}
	src, r, err := o.env.Get(ctx, engine.Ref{ID: noteID})
	if err != nil {
		return RowOut{}, err
	}
	if r.Deleted {
		return RowOut{}, errs.New(errs.NotFound, noteID.String()+" is deleted")
	}
	if phrase != "" && !noteQuotes(r, leakcheck.Words(phrase)) {
		return RowOut{}, errs.New(errs.InvalidInput, "leaks allow: "+noteID.String()+" does not hold that passage word for word, so it cannot block it").
			WithHint("copy the passage as the note has it, or use the --match-hash the refusal prints")
	}
	path := ""
	if strings.TrimSpace(in.File) != "" {
		fa, ok := o.env.(FileAllower)
		if !ok {
			return RowOut{}, errs.New(errs.Unsupported, "leaks allow --file: not available here")
		}
		if path, err = fa.RepoFilePath(ctx, in.File); err != nil {
			return RowOut{}, err
		}
	}
	fields := map[string]value.Value{aHash: value.Int(int64(hash))}
	if err = optTexts(fields, map[string]string{aField: textAllowField, aRuleID: leakcheck.TextAllowRule, aReason: strings.TrimSpace(in.Reason), aPath: path}); err != nil {
		return RowOut{}, err
	}
	if fields[aRowID], err = ref(noteID); err != nil {
		return RowOut{}, err
	}
	w, err := o.env.Create(ctx, src, schema.TableSecretAllows, fields)
	if err != nil {
		return RowOut{}, err
	}
	where := "anywhere"
	if path != "" {
		where = "in " + path
	}
	return RowOut{ID: w.Row.ID.String(), Store: string(src),
		Human: fmt.Sprintf("✓ %s: this passage of %s may ship %s (%s), saved in %s with the note; lore sync shares it, lore secrets allow rm --id %s removes it\n",
			short(w.Row.ID), noteID.String(), where, strings.TrimSpace(in.Reason), o.place(src), w.Row.ID.String())}, nil
}

// textAllowField is the field a text allow names: the passage may be in
// any text field of the note. No secret allow names it, so the secret
// scanner never matches a text allow.
const textAllowField = "text"

// noteQuotes reports whether one text field of r holds words in a row.
func noteQuotes(r row.Row, words []string) bool {
	want := strings.Join(words, " ")
	for _, v := range r.Fields {
		switch v.(type) {
		case value.Text, value.JSON:
			if strings.Contains(" "+strings.Join(leakcheck.Words(v.String()), " ")+" ", " "+want+" ") {
				return true
			}
		}
	}
	return false
}
