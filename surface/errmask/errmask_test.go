package errmask

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// fakeToken is a synthetic AWS access key id, built at run time.
func fakeToken() string { return "AK" + "IA" + "Q4X7Z2R9T6Y3W8V5" }

// TestEnvelopeMasksEveryPersonText (devco R-11): message, hint, question
// and the suggested next command never carry a typed secret; the code and
// exit stay, and the error value itself is not changed.
func TestEnvelopeMasksEveryPersonText(t *testing.T) {
	tok := fakeToken()
	err := errs.New(errs.InvalidIdentifier, `task: "`+tok+`" is not a task id`).
		WithHint("try lore task show " + tok)
	env := Of(err)
	b, mErr := json.Marshal(env)
	if mErr != nil {
		t.Fatal(mErr)
	}
	if strings.Contains(string(b), tok) {
		t.Fatalf("token in envelope: %s", b)
	}
	if env.Error.Code != errs.InvalidIdentifier || !strings.Contains(env.Error.Message, "is not a task id") {
		t.Fatalf("envelope lost its meaning: %+v", env.Error)
	}
	if !strings.Contains(errs.EnvelopeOf(err).Error.Message, tok) {
		t.Fatal("the error value itself was changed")
	}
	next := errs.Envelope{Error: errs.Body{Next: &errs.Next{Command: "lore show", Args: []string{tok}, AddArgsOneOf: [][]string{{"--to", tok}}}}}
	m := Envelope(next)
	if strings.Contains(strings.Join(m.Error.Next.Args, " ")+strings.Join(m.Error.Next.AddArgsOneOf[0], " "), tok) {
		t.Fatalf("token in next: %+v", m.Error.Next)
	}
	if next.Error.Next.Args[0] != tok {
		t.Fatal("caller's next args were changed in place")
	}
}
