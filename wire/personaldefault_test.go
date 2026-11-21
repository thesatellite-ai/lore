package wire

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsask "github.com/thesatellite-ai/lore/ops/ask"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestAskReadsPersonalStoreOutsideRepo pins devco:D-828 (bug hunt H-86):
// `lore ask` and `lore search` from a folder that is no repo, with no
// --store and no setup covering the folder, read the personal store (its
// folders too, as --store me) and say so in one line. Before it they
// refused E_NOT_A_STORE and an agent spent a call learning --store me.
// Other reads keep the refusal; with no personal store ask and search
// refuse as before; a named --store prints no note.
func TestAskReadsPersonalStoreOutsideRepo(t *testing.T) {
	t.Parallel()
	ctx := background()
	home := t.TempDir()
	bare := filepath.Join(t.TempDir(), "me.git")
	gitIn(t, t.TempDir(), nil, "init", "-q", "--bare", "-b", "main", bare)
	plain := t.TempDir()
	a := newTestAppHome(t, plain, home)
	if _, err := a.InitStore(ctx, opsstores.InitIn{Name: opsstores.PersonalName, URL: bare, RepoOptional: true}); err != nil {
		t.Fatalf("store init me on a bare repo: %v", err)
	}
	add, _ := a.Registry().Lookup("memory.add")
	if _, err := a.Run(ctx, add, op.JSONDecoder{Body: []byte(`{"body":"Status update harbor: live with 9 customers.","to":"me/work","create":true}`)}, op.Caller{Yes: true}); err != nil {
		t.Fatalf("memory add --to me/work: %v", err)
	}
	run := func(h, name, in string) (op.Result, error) {
		t.Helper()
		b := newTestAppHome(t, plain, h)
		e, ok := b.Registry().Lookup(name)
		if !ok {
			t.Fatalf("no op %s", name)
		}
		return b.Run(ctx, e, op.JSONDecoder{Body: []byte(in)}, op.Caller{})
	}
	for _, r := range []struct{ op, in string }{
		{opsask.OpRun, `{"query":"what is the status of harbor"}`},
		{opssearch.OpRun, `{"query":"harbor"}`},
	} {
		res, err := run(home, r.op, r.in)
		if err != nil {
			t.Fatalf("%s %s in a plain folder: %v, want the personal store read", r.op, r.in, err)
		}
		text, _ := res.Text()
		if !strings.Contains(text, "live with 9 customers") || !strings.Contains(text, PersonalDefaultNote) {
			t.Errorf("%s in a plain folder: want the me/work row and the note, got %q", r.op, text)
		}
		if out, _ := res.JSON(); !strings.Contains(string(out), "live with 9 customers") {
			t.Errorf("%s --json in a plain folder misses the me/work row: %s", r.op, out)
		}
		// a named store: no note
		named := strings.Replace(r.in, "{", `{"store":"me",`, 1)
		res, err = run(home, r.op, named)
		if err != nil {
			t.Fatalf("%s %s: %v", r.op, named, err)
		}
		if text, _ := res.Text(); strings.Contains(text, PersonalDefaultNote) {
			t.Errorf("%s with --store me: no note expected, got %q", r.op, text)
		}
		// no personal store: refused as before, the hint names no --store me
		_, err = run(t.TempDir(), r.op, r.in)
		var le *errs.Error
		if !errors.As(err, &le) || le.Code != errs.NotAStore || strings.Contains(le.Hint, "--store me") {
			t.Errorf("%s with no personal store: want E_NOT_A_STORE without --store me, got %v", r.op, err)
		}
	}
	// an empty answer: what was searched and the tries come before the note
	// (the empty-answer hook reads the answer's last line), and every try
	// names --store me, so it runs in the home folder (resource list alone
	// refuses there)
	for _, r := range []struct{ op, in string }{
		{opsask.OpRun, `{"query":"who is zed-quill"}`},
		{opssearch.OpRun, `{"query":"zedquill"}`},
	} {
		res, err := run(home, r.op, r.in)
		if err != nil {
			t.Fatalf("%s %s in a plain folder: %v", r.op, r.in, err)
		}
		text, _ := res.Text()
		if !strings.Contains(text, "  searched: me") || !strings.HasSuffix(strings.TrimSpace(text), PersonalDefaultNote) {
			t.Errorf("%s empty in a plain folder: want searched: then the note last, got %q", r.op, text)
		}
		tries := 0
		for _, line := range strings.Split(text, "\n") {
			if cmd, ok := strings.CutPrefix(line, "  try: lore "); ok {
				tries++
				if !strings.Contains(cmd, "--store me") {
					t.Errorf("%s empty in a plain folder: try %q does not name --store me", r.op, cmd)
				}
			}
		}
		if tries == 0 {
			t.Errorf("%s empty in a plain folder: no try lines: %q", r.op, text)
		}
	}
	// any other read keeps its refusal (its hint leads with --store me)
	_, err := run(home, "memory.list", `{}`)
	var le *errs.Error
	if !errors.As(err, &le) || le.Code != errs.NotAStore || !strings.HasPrefix(le.Hint, "add --store me ") {
		t.Errorf("memory list in a plain folder: want E_NOT_A_STORE leading with --store me, got %v", err)
	}
}
