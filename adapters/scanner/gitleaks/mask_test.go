package gitleaks

import (
	"strings"
	"testing"
)

// fakeGitHubToken is a synthetic token in the GitHub classic shape, built
// at run time so no source line holds one.
func fakeGitHubToken() string { return "gh" + "p_" + "aB3dE9fG1hJ2kL8mN4pQ7rS5tU6vW0xY1zC2" }

// TestMaskTextHidesTypedTokens (devco R-11): a secret-shaped value inside
// an error sentence is replaced by its rule and hash, every other word of
// the sentence stays, and text without a secret comes back unchanged.
func TestMaskTextHidesTypedTokens(t *testing.T) {
	tok := fakeGitHubToken()
	got := MaskText("no store named " + tok + " is attached")
	if strings.Contains(got, tok) || strings.Contains(got, tok[4:12]) {
		t.Fatalf("token echoed: %q", got)
	}
	if !strings.HasPrefix(got, "no store named "+MaskedOpen) || !strings.HasSuffix(got, MaskedClose+" is attached") {
		t.Fatalf("sentence not kept around the preview: %q", got)
	}
	plain := "mem_01a1a5aca7f57659be7320c5f4b7ffff is not a row id"
	if MaskText(plain) != plain || HasSecret(plain) {
		t.Fatalf("plain text changed: %q", MaskText(plain))
	}
	if !HasSecret("x\n" + tok + "\ny") {
		t.Fatal("HasSecret missed a token on its own line")
	}
}
