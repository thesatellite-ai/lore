package setup

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestFoldersDropRepeats: a repeated folder is saved once, the first of
// each kept in order, on both writers of a setup's folders: the folders
// step's answer (Apply, withoutDuplicateFolders) and `setup edit
// --folder` (edited). Inputs: no repeat, an exact repeat, a repeat with
// spaces around it, three of one, two repeated, an empty list. Each
// dropped folder is named once in a one-line warning (setup apply and
// setup edit output, JSON "warnings"); with no repeat there is none and
// JSON leaves "warnings" out.
func TestFoldersDropRepeats(t *testing.T) {
	cases := []struct {
		in, want, repeated []string
	}{
		{[]string{"~/code/**", "/srv/acme/**"}, []string{"~/code/**", "/srv/acme/**"}, nil},
		{[]string{"~/code/**", "/srv/acme/**", "~/code/**"}, []string{"~/code/**", "/srv/acme/**"}, []string{"~/code/**"}},
		{[]string{"~/code/**", " ~/code/** "}, []string{"~/code/**"}, []string{"~/code/**"}},
		{[]string{"/a/**", "/a/**", "/a/**", "/b/**"}, []string{"/a/**", "/b/**"}, []string{"/a/**"}},
		{[]string{"/b/**", "/a/**", "/a/**", "/b/**"}, []string{"/b/**", "/a/**"}, []string{"/a/**", "/b/**"}},
		{[]string{}, []string{}, nil},
	}
	for _, c := range cases {
		in := onboard.Answers{
			onboard.QIDFolders: {IsList: true, List: slices.Clone(c.in)},
			onboard.QIDDefault: {IsBool: true, Bool: true},
		}
		got, repeated := withoutDuplicateFolders(in)
		if !slices.Equal(repeated, c.repeated) {
			t.Errorf("step answers %q: repeated %q, want %q", c.in, repeated, c.repeated)
		}
		if !slices.Equal(got[onboard.QIDFolders].List, c.want) || !got[onboard.QIDDefault].Bool {
			t.Errorf("step answers %q: got %+v, want folders %q and the other answers kept", c.in, got, c.want)
		}
		if !slices.Equal(in[onboard.QIDFolders].List, c.in) {
			t.Errorf("step answers %q: the caller's map was changed to %q", c.in, in[onboard.QIDFolders].List)
		}
		env := newFakeEnv(onboard.SetupEntry{Name: "acme-hub", Kind: onboard.KindSolo})
		out, err := newTestEngine(env).Edit(bg, EditIn{Name: "acme-hub", Folders: slices.Clone(c.in)})
		if err != nil {
			t.Fatalf("edit --folder %q: %v", c.in, err)
		}
		if len(out.Warnings) != len(c.repeated) {
			t.Errorf("edit --folder %q: warnings %q, want one per repeated folder %q", c.in, out.Warnings, c.repeated)
		}
		for i, w := range out.Warnings {
			if want := `folder "` + c.repeated[i] + `" was given more than once; it is saved once`; w != want || strings.Contains(w, "\n") {
				t.Errorf("edit --folder %q: warning %q, want %q", c.in, w, want)
			}
		}
		b, jerr := json.Marshal(out)
		if jerr != nil {
			t.Fatal(jerr)
		}
		if strings.Contains(string(b), `"warnings"`) != (len(c.repeated) > 0) {
			t.Errorf("edit --folder %q: JSON %s", c.in, b)
		}
		if s, _ := env.store.cfg.Setup("acme-hub"); !slices.Equal(s.Folders, c.want) && (len(c.want) > 0 || len(s.Folders) > 0) {
			t.Errorf("edit --folder %q saved %q, want %q", c.in, s.Folders, c.want)
		}
	}
	if got, repeated := withoutDuplicateFolders(nil); got != nil || repeated != nil {
		t.Errorf("no answers: %+v, want nil", got)
	}
}
