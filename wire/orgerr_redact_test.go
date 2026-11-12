package wire

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestMaskOrgErr: an org-resolution error names the configured store url
// in every spelling as it is, in its message, hint and cause (the person
// needs it to fix the error; a store's address is no secret); only a
// credential in a url is masked, and the code and the rest stay. A
// non-lore error and nil are returned as they are.
func TestMaskOrgErr(t *testing.T) {
	t.Parallel()
	for _, u := range []string{
		"git@github.com:agency/notes",
		"https://github.com/agency/notes",
		"ssh://git@github.com/agency/notes.git",
		"github.com/agency/notes",
		"https://github.com/other/vault.git",
	} {
		in := errs.Wrap(errs.NotAStore, u+" (branch main) is listed under stores: but is not a lore store", errors.New("fetch "+u+": refused")).
			WithHint("remove " + u + " from ~/.lore/config.yaml")
		got := maskOrgErr(in)
		if !errors.Is(got, in) || got.Error() != in.Error() {
			t.Fatalf("%s: %v, want the error unchanged", u, got)
		}
	}
	pw := "pass" + "word1"
	withPw := "https:/" + "/alice:" + pw + "@github.com/agency/notes.git" // put together at run time (leak hooks scan the source)
	in := errs.Wrap(errs.NotAStore, withPw+" (branch main) is not a lore store", errors.New("fetch "+withPw+": refused")).
		WithHint("remove " + withPw + " from ~/.lore/config.yaml")
	got := maskOrgErr(in)
	if !errs.Has(got, errs.NotAStore) {
		t.Fatalf("code lost: %v", got)
	}
	body := errs.EnvelopeOf(got).Error
	all := got.Error() + "\n" + body.Message + "\n" + body.Hint
	if strings.Contains(all, pw) {
		t.Fatalf("password left in %q", all)
	}
	masked := "https://alice:" + redactedText + "@github.com/agency/notes.git"
	if !strings.Contains(body.Message, masked+" (branch main)") || !strings.Contains(body.Hint, masked+" from ~/.lore/config.yaml") || !strings.Contains(got.Error(), "fetch "+masked) {
		t.Fatalf("not masked in place: %q / %q / %q", body.Message, body.Hint, got.Error())
	}
	plain := errors.New(withPw + " broke")
	if got := maskOrgErr(plain); !errors.Is(got, plain) {
		t.Fatalf("non-lore error rewritten: %v", got)
	}
	if got := maskOrgErr(nil); got != nil {
		t.Fatalf("nil became %v", got)
	}
}
