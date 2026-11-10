package orgresolve

import (
	"fmt"
	"strings"
	"testing"
)

// A refusal is only useful if it names the choices: candidates are part
// of the message, and the kind survives wrapping.
func TestErrorNamesCandidates(t *testing.T) {
	cases := []struct {
		e    Error
		want string
	}{
		{Error{Kind: KindUnknownEntry, Message: "no org entry named x"}, "no org entry named x"},
		{Error{Kind: KindAmbiguous, Message: "two orgs match", Candidates: []string{"agency"}}, "two orgs match (candidates: agency)"},
		{Error{Kind: KindAmbiguous, Message: "two orgs match", Candidates: []string{"agency", "client2"}, Hint: "run lore org use"}, "two orgs match (candidates: agency, client2)"},
		{Error{Kind: KindAmbiguous, Message: "", Candidates: []string{}}, ""},
	}
	for _, tc := range cases {
		e := tc.e
		if got := e.Error(); got != tc.want {
			t.Errorf("Error() = %q, want %q", got, tc.want)
		}
		if strings.Contains(e.Error(), e.Hint) && e.Hint != "" {
			t.Errorf("the hint is printed separately, not in the message: %q", e.Error())
		}
		if KindOf(fmt.Errorf("resolve: %w", &e)) != e.Kind {
			t.Errorf("kind lost through wrapping")
		}
	}
	if KindOf(fmt.Errorf("plain")) != 0 || KindOf(nil) != 0 {
		t.Fatal("not an *Error: kind 0")
	}
}
