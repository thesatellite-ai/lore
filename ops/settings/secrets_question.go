package settings

import (
	"context"
	"strings"
)

// The confirm questions of the always-ask secret rule operations: what
// changes and where, never a pattern or a reason (the question travels in
// E_NEEDS_CONFIRM's message, and a pattern may itself look like a secret).

// questionWordMax caps a name the caller typed inside a question.
const questionWordMax = 64

// questionWord is s trimmed, one line, cut at questionWordMax.
func questionWord(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > questionWordMax {
		s = string(r[:questionWordMax]) + "…"
	}
	return s
}

// questionPlace is the place an input's --to names ("repo" by default).
func questionPlace(to string) string {
	if p := questionWord(to); p != "" {
		return p
	}
	return "repo"
}

func (ops) ruleAddQuestion(_ context.Context, in RuleAddIn) (string, error) {
	return "Add secret rule " + questionWord(in.RuleID) + " in " + questionPlace(in.To) +
		"? Every later write there is scanned with it (a match follows secrets.on_detect: ask, redact or refuse).", nil
}

func (ops) ruleEditQuestion(_ context.Context, in RuleAddIn) (string, error) {
	return "Change secret rule " + questionWord(in.RuleID) + " in " + questionPlace(in.To) +
		"? Later writes there are scanned with the changed rule; rows already stored are not rescanned.", nil
}

func (ops) ruleOffQuestion(_ context.Context, in RuleOffIn) (string, error) {
	return "Switch secret rule " + questionWord(in.RuleID) + " off in " + questionPlace(in.To) +
		"? Writes there are no longer checked for it (lore doctor lists it as off).", nil
}

func (ops) ruleOnQuestion(_ context.Context, in RuleOnIn) (string, error) {
	return "Switch secret rule " + questionWord(in.RuleID) + " back on in " + questionPlace(in.To) +
		"? Later writes there are checked for it again.", nil
}

func (o ops) allowRmQuestion(ctx context.Context, in AllowRmIn) (string, error) {
	if _, _, _, err := o.allowFind(ctx, in.ID); err != nil {
		return "", err // a bad or missing id is reported, not asked about
	}
	return "Remove allow entry " + questionWord(in.ID) +
		"? The finding it covers is reported again on the next write or scan of its row.", nil
}
