package onboard

// push.go — the push mode question of the personal store step (4.4) and
// the Start a team step (4.6): when a store's writes reach its remote
// (logic/storepush: now | batch | manual). The setup offers the mode only;
// batch keeps the default interval (storepush.DefaultPushEvery), and
// push_every and compaction stay with `lore store set`.
//
//	"" or now ─▶ no setting (now is the default: nothing is written)
//	batch     ─▶ push=batch
//	manual    ─▶ push=manual
//	other     ─▶ error naming the value and the three modes

import (
	"strings"

	"github.com/thesatellite-ai/lore/logic/storepush"
)

// pushChoices are the push question's options, in storepush.Modes order.
func pushChoices() ChoiceSource {
	every := storepush.FormatAge(storepush.DefaultPushEvery)
	return ChoiceSource{Static: []Choice{
		{Value: string(storepush.ModeNow), Label: "Now", Detail: "each write is pushed at once (default)"},
		{Value: string(storepush.ModeBatch), Label: "Batch", Detail: "writes wait locally; one push every " + every + " and on lore sync"},
		{Value: string(storepush.ModeManual), Label: "Manual", Detail: "writes wait locally until lore sync"},
	}}
}

// PushSettings is the store settings a push answer asks for: none for an
// empty answer or now (the default needs no key, so choosing it writes
// nothing), push=<mode> for batch and manual. Any other value is refused
// naming it.
func PushSettings(answer string) ([]storepush.Assignment, error) {
	v := strings.ToLower(strings.TrimSpace(answer))
	if v == "" || v == string(storepush.ModeNow) {
		return nil, nil
	}
	m, err := storepush.ParseMode(v)
	if err != nil {
		return nil, err
	}
	return []storepush.Assignment{{Key: storepush.KeyPush, Value: string(m)}}, nil
}
