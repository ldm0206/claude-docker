package auth

import "testing"

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
