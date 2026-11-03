package main

// The lazy done → merged check (F9) must not look as if it ran when the
// linked store could not be reached: the command still succeeds, but one
// warning line on stderr says the merge state was not checked and why.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"saas/pkg/aicoder/orgresolve"
)

func TestGroupMergedCheckWarnsWhenStoreUnreachable(t *testing.T) {
	t.Parallel()
	unreachable := errors.New("fetch git@example.com:acme/store.git: connection refused\nexit status 128")
	cases := []struct {
		name     string
		links    []orgresolve.Link
		openErr  error
		wantWarn bool
	}{
		{name: "no links: nothing to check, silent", links: nil, openErr: unreachable, wantWarn: false},
		{name: "linked, store unreachable: one warning", links: []orgresolve.Link{{Folder: "acme"}}, openErr: unreachable, wantWarn: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			opened := false
			open := func(context.Context, *coordWorld) (coordTarget, error) {
				opened = true
				return coordTarget{}, tc.openErr
			}
			groupMergedCheckIn(context.Background(), &out, &coordWorld{Links: tc.links}, open)
			got := out.String()
			if !tc.wantWarn {
				if got != "" || opened {
					t.Fatalf("want silence and no store open, got opened=%v %q", opened, got)
				}
				return
			}
			if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
				t.Fatalf("want exactly one warning line, got %q", got)
			}
			for _, want := range []string{mergeUncheckedPrefix, "open the linked store", "connection refused exit status 128", "not merged yet"} {
				if !strings.Contains(got, want) {
					t.Fatalf("warning %q lacks %q", got, want)
				}
			}
		})
	}
}
