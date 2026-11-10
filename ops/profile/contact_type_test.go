package profile

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// TestContactTypeList: the built-ins with no rows (TS-2: nothing seeded),
// then changed and added rows, and --retired.
func TestContactTypeList(t *testing.T) {
	ctx := context.Background()
	f, _, _ := world(t)
	out, err := contactTypeList(ctx, f, ContactTypeListIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Types) != len(teamprofile.BuiltinContactTypes()) || len(f.All(schema.TableContactTypes)) != 0 {
		t.Fatalf("built-ins: %d types, %d rows", len(out.Types), len(f.All(schema.TableContactTypes)))
	}
	for _, ty := range out.Types {
		if ty.Origin != OriginBuiltin || ty.ID != "" {
			t.Fatalf("%+v", ty)
		}
	}
	if _, err := contactTypeEdit(ctx, f, ContactTypeEditIn{Name: "github", Label: "GitHub.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := contactTypeAdd(ctx, f, ContactTypeAddIn{Name: "bitbucket", Kind: schema.ContactKindHandle, Identity: true, URLTemplate: "https://bitbucket.org/{value}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := contactTypeEdit(ctx, f, ContactTypeEditIn{Name: "discord", Retire: true}); err != nil {
		t.Fatal(err)
	}
	out, _ = contactTypeList(ctx, f, ContactTypeListIn{})
	got := map[string]TypeOut{}
	for _, ty := range out.Types {
		got[ty.Name] = ty
	}
	if g := got["github"]; g.Origin != OriginChanged || g.Label != "GitHub.com" || !g.Identity || g.Kind != schema.ContactKindHandle || g.URLTemplate == "" || g.ID == "" {
		t.Fatalf("github %+v", g)
	}
	if b := got["bitbucket"]; b.Origin != OriginAdded || !b.Identity || out.Types[len(out.Types)-1].Name != "bitbucket" {
		t.Fatalf("bitbucket %+v", b)
	}
	if _, listed := got["discord"]; listed {
		t.Fatal("retired type listed without --retired")
	}
	out, _ = contactTypeList(ctx, f, ContactTypeListIn{Retired: true})
	if !strings.Contains(out.Text(), "(retired)") {
		t.Fatalf("--retired:\n%s", out.Text())
	}
	// an added identity type works for contacts
	if _, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "bitbucket", Value: "@MiraP", Identity: true}); err != nil {
		t.Fatal(err)
	}
	// unretire
	if _, err := contactTypeEdit(ctx, f, ContactTypeEditIn{Name: "discord", Unretire: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "discord", Value: "mira"}); err != nil {
		t.Fatalf("unretired: %v", err)
	}
}

// TestContactTypeBuiltinIdentityFixed: a row (written by hand or by a
// future op) may not change kind or identity of github, gitlab, email
// (2.3); a non-identity built-in may be changed.
func TestContactTypeBuiltinIdentityFixed(t *testing.T) {
	mk := func(name, kind string, ident bool) row.Row {
		n, _ := value.TextOf(name)
		k, _ := value.TextOf(kind)
		return row.Row{Table: schema.TableContactTypes, Fields: map[string]value.Value{
			schema.FieldContactTypeName: n, schema.FieldContactTypeKind: k, schema.FieldContactTypeIdentity: value.BoolOf(ident)}}
	}
	types := effectiveTypes([]row.Row{mk("email", schema.ContactKindText, false), mk("phone", schema.ContactKindText, true)})
	e, _ := findType(types, "email")
	if e.Kind != schema.ContactKindEmail || !e.Identity || e.Origin != OriginChanged {
		t.Fatalf("email overridden: %+v", e.ContactType)
	}
	p, _ := findType(types, "phone")
	if p.Kind != schema.ContactKindText || !p.Identity {
		t.Fatalf("phone row ignored: %+v", p.ContactType)
	}
}

// TestContactTypeRefusals: the add and edit refusal matrix.
func TestContactTypeRefusals(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		run  func(Env) error
		code errs.Code
	}{
		{"add a built-in name", func(e Env) error {
			_, err := contactTypeAdd(ctx, e, ContactTypeAddIn{Name: "github", Kind: schema.ContactKindHandle})
			return err
		}, errs.NameTaken},
		{"add twice", func(e Env) error {
			in := ContactTypeAddIn{Name: "bitbucket", Kind: schema.ContactKindHandle}
			if _, err := contactTypeAdd(ctx, e, in); err != nil {
				return err
			}
			_, err := contactTypeAdd(ctx, e, in)
			return err
		}, errs.NameTaken},
		{"add bad name", func(e Env) error {
			_, err := contactTypeAdd(ctx, e, ContactTypeAddIn{Name: "Bit Bucket", Kind: schema.ContactKindHandle})
			return err
		}, errs.InvalidInput},
		{"add bad kind", func(e Env) error {
			_, err := contactTypeAdd(ctx, e, ContactTypeAddIn{Name: "bitbucket", Kind: "login"})
			return err
		}, errs.InvalidInput},
		{"add template without placeholder", func(e Env) error {
			_, err := contactTypeAdd(ctx, e, ContactTypeAddIn{Name: "bitbucket", Kind: schema.ContactKindHandle, URLTemplate: "https://bitbucket.org/"})
			return err
		}, errs.InvalidInput},
		{"template with credentials", func(e Env) error {
			// built at run time: no credential-shaped literal in the tree
			tpl := "https://" + strings.Join([]string{"u", "p"}, ":") + "@bitbucket.org/{value}"
			_, err := contactTypeAdd(ctx, e, ContactTypeAddIn{Name: "bitbucket", Kind: schema.ContactKindHandle, URLTemplate: tpl})
			return err
		}, errs.URLUserinfo},
		{"edit unknown", func(e Env) error {
			_, err := contactTypeEdit(ctx, e, ContactTypeEditIn{Name: "myspace", Label: "x"})
			return err
		}, errs.ContactType},
		{"edit nothing", func(e Env) error {
			_, err := contactTypeEdit(ctx, e, ContactTypeEditIn{Name: "github"})
			return err
		}, errs.InvalidInput},
		{"edit retire and unretire", func(e Env) error {
			_, err := contactTypeEdit(ctx, e, ContactTypeEditIn{Name: "github", Retire: true, Unretire: true})
			return err
		}, errs.InvalidInput},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, _, _ := world(t)
			wantCode(t, c.name, c.run(f), c.code)
		})
	}
	// reads are open to agents; writes need agents.may_manage_people (TS-4)
	f, _, _ := world(t)
	f.Agent = true
	if _, err := contactTypeList(ctx, f, ContactTypeListIn{}); err != nil {
		t.Fatalf("agent read: %v", err)
	}
	_, err := contactTypeAdd(ctx, f, ContactTypeAddIn{Name: "bitbucket", Kind: schema.ContactKindHandle})
	wantCode(t, "agent add", err, errs.PolicyDenied)
	f.MayManagePeople = true
	if _, err := contactTypeAdd(ctx, f, ContactTypeAddIn{Name: "bitbucket", Kind: schema.ContactKindHandle}); err != nil {
		t.Fatalf("agent with policy: %v", err)
	}
}

// TestContactTypeOpsRegister: Ops defines every op under its
// profilestore name (Define panics on a bad tag).
func TestContactTypeOpsRegister(t *testing.T) {
	f, _, _ := world(t)
	want := map[string]bool{}
	for _, n := range []string{profilestore.OpActorEdit, profilestore.OpContactAdd, profilestore.OpContactEdit, profilestore.OpContactRemove,
		profilestore.OpContactList, profilestore.OpContactTypeList, profilestore.OpContactTypeAdd, profilestore.OpContactTypeEdit} {
		want[n] = true
	}
	for _, e := range Ops(f) {
		if !want[e.Spec().Name] || !profilestore.IsDirect(e.Spec().Name) {
			t.Fatalf("unexpected op %s", e.Spec().Name)
		}
		delete(want, e.Spec().Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing ops %v", want)
	}
	// the registry's own checks: error codes registered, routes unique
	if _, err := op.NewRegistry(Ops(f)...); err != nil {
		t.Fatal(err)
	}
}
