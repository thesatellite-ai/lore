package gitremote

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestTransportErrorMessageAndUnwrap(t *testing.T) {
	e := &TransportError{Op: "push", Err: ErrUnconfirmed, Stderr: "fatal: unable to access"}
	msg := e.Error()
	for _, w := range []string{"gitremote: push", ErrUnconfirmed.Error(), "fatal: unable to access"} {
		if !strings.Contains(msg, w) {
			t.Fatalf("%q missing %q", msg, w)
		}
	}
	if !errors.Is(fmt.Errorf("wrap: %w", e), ErrUnconfirmed) {
		t.Fatal("the sentinel survives wrapping")
	}
	var te *TransportError
	if !errors.As(fmt.Errorf("wrap: %w", e), &te) || te.Op != "push" {
		t.Fatal("errors.As finds the transport error")
	}
	if errors.Is(&TransportError{Op: "fetch", Err: ErrUnconfirmed}, errors.New("other")) {
		t.Fatal("unrelated errors do not match")
	}
}
