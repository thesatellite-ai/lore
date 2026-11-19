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

// TestNoInternalPackagePrefix (devco D-641): no envelope a person reads
// starts a message, or a ": " join in it, with an internal Go package name
// ("wire: this repository has no lore project"); prefixes naming a lore
// command or git are kept.
func TestNoInternalPackagePrefix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"wire: this repository has no lore project", "this repository has no lore project"},
		{"store acme: wire: write the actor row to store me: engine: no store named me is attached", "store acme: write the actor row to store me: no store named me is attached"},
		{"rowfs: meta: read _meta.json: bad json", "read _meta.json: bad json"},
		{"git: the folder is not inside a git work tree", "git: the folder is not inside a git work tree"},
		{"migrate: nothing to do", "migrate: nothing to do"},
		{"wire", "wire"},
		{"the row: wire: x", "the row: x"},
	}
	for _, c := range cases {
		got := Of(errs.New(errs.NotFound, c.in)).Error.Message
		if got != c.want {
			t.Errorf("message %q shows as %q, want %q", c.in, got, c.want)
		}
		for _, p := range internalPackages {
			if strings.HasPrefix(got, p+": ") || strings.Contains(got, ": "+p+": ") {
				t.Errorf("message %q still shows the internal prefix %q: %q", c.in, p, got)
			}
		}
	}
}
