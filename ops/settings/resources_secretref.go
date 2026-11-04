package settings

import (
	"errors"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// secret_ref fields (RESOURCES 3.1, 3.5 item 10, 3.8):
//
//	field add --type secret_ref   internal unless --no-internal (3.1)
//	field add <credential-ish>    refused unless --type secret_ref (3.1)
//	--set f=<value>               bare path → <default manager>://path,
//	                              shape (value.SecretRefOf: length, spaces,
//	                              random-looking part); the manager list
//	                              check (E_REF_SCHEME) joins when logic/refs
//	                              exports Managers (ov8, REFERENCE FORMAT)

// defaultSecretManager is the manager a bare secret_ref path means when no
// kind or policy names one (RESOURCES 3.5: "default from policy"; the
// samples' gopass). DECISION RES-SR1: until a secrets.default_manager key
// and the kind secret_manager column exist, gopass.
const defaultSecretManager = "gopass"

// credentialParts are field-name parts that look like a credential: such a
// field must be a secret_ref (RESOURCES 3.1 reserved names).
var credentialParts = []string{"password", "passwd", "secret", "token", "api_key", "apikey", "private_key", "credential"}

// credentialName refuses a credential-looking field name unless the field
// is a secret_ref, so a password is never typed into a text field.
func credentialName(field string, isSecretRef bool) error {
	if isSecretRef {
		return nil
	}
	for _, p := range credentialParts {
		if strings.Contains(field, p) {
			// E_RESOURCE_NAME: a credential-looking name is a name refusal (RES 3.1).
			return errs.New(errs.ResourceName, "field "+quote(field)+" looks like a credential ("+p+"); lore never stores a secret").
				WithHint("store it in a secret manager and add the field with --type secret_ref (it holds the entry name)")
		}
	}
	return nil
}

// secretRefInternal is the internal flag of a new field: a secret_ref is
// internal unless --no-internal (RESOURCES 3.1); --no-internal on another
// type is refused (it would mean nothing).
func secretRefInternal(isSecretRef, internal, noInternal bool) (bool, error) {
	switch {
	case internal && noInternal:
		return false, errs.New(errs.InvalidInput, "give --internal or --no-internal, not both")
	case noInternal && !isSecretRef:
		return false, errs.New(errs.InvalidInput, "--no-internal belongs to --type secret_ref (other fields are not internal unless --internal)")
	case isSecretRef:
		return !noInternal, nil
	}
	return internal, nil
}

// secretRefNote is the field add line's tail for a secret_ref.
func secretRefNote(isSecretRef, internal bool) string {
	switch {
	case !isSecretRef:
		return ""
	case internal:
		return ", internal by default: an entry NAME in a secret manager, never the key"
	}
	return ", not internal: an entry NAME in a secret manager, never the key"
}

// secretRefValue parses a --set value of a secret_ref field: a bare path
// gets the default manager, then the shape check and the manager list.
// Errors never repeat the value (it may be a pasted secret).
// where names the field in the message ("host h1: field ssh").
func secretRefValue(where, raw string) (value.SecretRef, error) {
	if !strings.Contains(raw, "://") {
		raw = defaultSecretManager + "://" + raw
	}
	v, err := value.SecretRefOf(raw)
	if err != nil {
		// value's own message never repeats the input.
		why := err.Error()
		var e *errs.Error
		if errors.As(err, &e) {
			why = strings.TrimPrefix(e.Message, "value: ")
		}
		return value.SecretRef{}, errs.New(errs.SecretDetected, where+" holds no entry name ("+why+")").
			WithHint("a secret_ref takes an entry name such as gopass://acme/hub_stag/ssh_deploy; keep the secret in the manager")
	}
	return v, nil
}
