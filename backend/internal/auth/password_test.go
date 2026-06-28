package auth

import (
	"strings"
	"testing"
)

func TestHashAndCheck(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !CheckPassword("correct horse", h) {
		t.Fatal("CheckPassword should accept the right password")
	}
	if CheckPassword("wrong", h) {
		t.Fatal("CheckPassword should reject a wrong password")
	}
}

// TestDecoyHashIsValidArgon2id pins that the package-level decoy hash is a
// well-formed argon2id string and that CheckPasswordDecoy does not panic /
// always returns. The decoy is used to equalize timing on the missing-user
// login path; it must be parseable by CheckPassword so the missing-user path
// spends the same ~argon2id time as a real wrong-password check.
func TestDecoyHashIsValidArgon2id(t *testing.T) {
	if !strings.HasPrefix(decoyHash, "$argon2id$") {
		t.Fatalf("decoyHash is not an argon2id hash: %q", decoyHash)
	}
	// A wrong password against the decoy must verify-false (not panic, not
	// verify-true). This mirrors how the login path uses the decoy.
	if CheckPassword("anything", decoyHash) {
		t.Fatal("CheckPassword against decoyHash should return false for any input")
	}
	// CheckPasswordDecoy must not panic; it discards its result.
	CheckPasswordDecoy("does-not-matter")
}
