package stores

import (
	"errors"
	"testing"
)

// F14 matrix: every --to form × kind × role × actor × TTY × store vs
// personal defaults × link unknown × 0/1/2 links.
func TestResolveTarget(t *testing.T) {
	acme := []TargetLink{{Folder: "acme"}}
	two := []TargetLink{{Folder: "acme"}, {Folder: "globex"}}
	twoDef := []TargetLink{{Folder: "acme", Default: true}, {Folder: "globex"}}
	shipped := map[string]string{"task": "northwind", "memory": "northwind", "rule": "repo", "decision": "repo"}
	type want struct {
		repo   bool
		folder string
		err    error
	}
	cases := []struct {
		name string
		in   TargetInputs
		want want
	}{
		{"to repo in client", TargetInputs{To: ToRepo, Org: "northwind", Role: RoleClient, Links: acme}, want{repo: true}},
		{"to repo by agent still allowed", TargetInputs{To: ToRepo, Org: "northwind", Role: RoleClient, Agent: true, Kind: "rule"}, want{repo: true}},
		{"to org one link", TargetInputs{To: ToOrg, Org: "northwind", Role: RoleClient, Links: acme}, want{folder: "acme"}},
		{"to org two links no default", TargetInputs{To: ToOrg, Org: "northwind", Role: RoleClient, Links: two}, want{err: ErrTwoLinksNoDefault}},
		{"to org two links default", TargetInputs{To: ToOrg, Org: "northwind", Role: RoleClient, Links: twoDef}, want{folder: "acme"}},
		{"to org no link own repo with committed project", TargetInputs{To: ToOrg, Org: "northwind", Role: RoleOwn, CommittedProject: "lore-tools"}, want{folder: "lore-tools"}},
		{"to org no link", TargetInputs{To: ToOrg, Org: "northwind", Role: RoleOwn}, want{err: ErrNoLinkedProject}},
		{"to org link unknown and no link", TargetInputs{To: ToOrg, Org: "northwind", Role: RoleClient, LinkUnknown: true}, want{err: ErrLinkUnknown}},
		{"to org/folder", TargetInputs{To: ToOrgFolder, Org: "northwind", Folder: "globex", Role: RoleClient, Links: acme}, want{folder: "globex"}},
		{"to org/folder while link unknown", TargetInputs{To: ToOrgFolder, Org: "northwind", Folder: "globex", LinkUnknown: true}, want{folder: "globex"}},
		{"no to, link unknown", TargetInputs{Org: "northwind", Role: RoleClient, LinkUnknown: true, Kind: "memory"}, want{err: ErrLinkUnknown}},
		{"no to, link unknown even when patterns say own", TargetInputs{Org: "northwind", Role: RoleOwn, LinkUnknown: true, Kind: "memory"}, want{err: ErrLinkUnknown}},
		{"no to, own repo", TargetInputs{Org: "northwind", Role: RoleOwn, Kind: "memory", StoreDefaults: shipped}, want{repo: true}},
		{"no to, no org", TargetInputs{Role: RoleNone, Kind: "memory"}, want{repo: true}},
		{"client memory → linked", TargetInputs{Org: "northwind", Role: RoleClient, Kind: "memory", Links: acme, StoreDefaults: shipped}, want{folder: "acme"}},
		{"client task plural key", TargetInputs{Org: "northwind", Role: RoleClient, Kind: "task", Links: acme, StoreDefaults: map[string]string{"tasks": "northwind"}}, want{folder: "acme"}},
		{"client kind not listed → linked", TargetInputs{Org: "northwind", Role: RoleClient, Kind: "comment", Links: acme, StoreDefaults: shipped}, want{folder: "acme"}},
		{"client no defaults at all → linked", TargetInputs{Org: "northwind", Role: RoleClient, Kind: "memory", Links: acme}, want{folder: "acme"}},
		{"client rule human tty → repo", TargetInputs{Org: "northwind", Role: RoleClient, Kind: "rule", Links: acme, StoreDefaults: shipped, TTY: true}, want{repo: true}},
		{"client rule no tty → refused", TargetInputs{Org: "northwind", Role: RoleClient, Kind: "rule", Links: acme, StoreDefaults: shipped}, want{err: ErrNeedsExplicitTo}},
		{"client decision agent → refused", TargetInputs{Org: "northwind", Role: RoleClient, Kind: "decision", Links: acme, StoreDefaults: shipped, TTY: true, Agent: true}, want{err: ErrNeedsExplicitTo}},
		{"client memory default repo by person is allowed", TargetInputs{Org: "northwind", Role: RoleClient, Kind: "memory", Links: acme, StoreDefaults: map[string]string{"memory": "repo"}}, want{repo: true}},
		{"store stricter beats personal repo", TargetInputs{Org: "northwind", Role: RoleClient, Kind: "memory", Links: acme, StoreDefaults: map[string]string{"memory": "northwind"}, PersonalDefaults: map[string]string{"memory": "repo"}}, want{folder: "acme"}},
		{"personal stricter beats store repo", TargetInputs{Org: "northwind", Role: RoleClient, Kind: "memory", Links: acme, StoreDefaults: map[string]string{"memory": "repo"}, PersonalDefaults: map[string]string{"memory": "northwind"}}, want{folder: "acme"}},
		{"personal only", TargetInputs{Org: "northwind", Role: RoleClient, Kind: "memory", Links: acme, PersonalDefaults: map[string]string{"memory": "northwind/globex"}}, want{folder: "globex"}},
		{"default naming another org falls back to the link", TargetInputs{Org: "northwind", Role: RoleClient, Kind: "memory", Links: acme, PersonalDefaults: map[string]string{"memory": "betalabs/x"}}, want{folder: "acme"}},
		{"client two links no default, no to", TargetInputs{Org: "northwind", Role: RoleClient, Kind: "memory", Links: two, StoreDefaults: shipped}, want{err: ErrTwoLinksNoDefault}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveTarget(tc.in)
			if tc.want.err != nil {
				if !errors.Is(err, tc.want.err) {
					t.Fatalf("err = %v, want %v", err, tc.want.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Repo != tc.want.repo || got.Folder != tc.want.folder {
				t.Fatalf("got %+v, want repo=%v folder=%q", got, tc.want.repo, tc.want.folder)
			}
			if got.Rule == "" {
				t.Fatal("every decision names its rule")
			}
		})
	}
}
