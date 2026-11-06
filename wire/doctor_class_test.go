package wire

import (
	"context"
	"errors"
	"testing"
)

// TestDoctorRepoClass (later 324): the repo_class row says why the repo is
// treated as a client repo without a positive answer: unwired check is a
// note, a failed check a warning naming the cause; a check that answered
// (client or not) prints nothing. DoctorChecks runs the row.
func TestDoctorRepoClass(t *testing.T) {
	t.Parallel()
	boom := errors.New("org links unreadable")
	cases := []struct {
		name  string
		hook  func(context.Context) (bool, error)
		level DoctorLevel
		want  string
	}{
		{"unwired", nil, DoctorNote, "the repo is treated as a client repo: no client-repo check is wired"},
		{"failed", func(context.Context) (bool, error) { return false, boom }, DoctorWarning,
			"the repo is treated as a client repo: whether it is one could not be checked (org links unreadable)"},
		{"client", func(context.Context) (bool, error) { return true, nil }, "", ""},
		{"internal", func(context.Context) (bool, error) { return false, nil }, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := openWith(t, gitRepo(t), Options{RepoIsClient: c.hook})
			got, err := a.DoctorRepoClass(background())
			if err != nil {
				t.Fatal(err)
			}
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("findings for an answered check: %+v", got)
				}
			} else if len(got) != 1 || got[0].Check != DoctorCheckRepoClass || got[0].Level != c.level || !got[0].Plain || got[0].Line() != c.want {
				t.Fatalf("findings %+v, want %s %q", got, c.level, c.want)
			}
			ran := false
			for _, r := range a.DoctorChecks(background(), false) {
				if r.Check == string(DoctorCheckRepoClass) {
					ran = true
					if len(r.Findings) != len(got) {
						t.Fatalf("DoctorChecks repo_class findings %+v", r.Findings)
					}
				}
			}
			if !ran {
				t.Fatal("DoctorChecks does not run repo_class")
			}
		})
	}
}
