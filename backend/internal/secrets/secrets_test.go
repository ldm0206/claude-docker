package secrets

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"testing"
)

// --- MasterKey tests ---

func TestMasterKey(t *testing.T) {
	t.Run("base64 of 32 bytes", func(t *testing.T) {
		raw := make([]byte, 32)
		for i := range raw {
			raw[i] = byte(i)
		}
		encoded := base64.StdEncoding.EncodeToString(raw)
		got, err := MasterKey(func(s string) (string, bool) {
			if s == "MASTER_KEY" {
				return encoded, true
			}
			return "", false
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 32 {
			t.Fatalf("expected 32 bytes, got %d", len(got))
		}
		for i := range raw {
			if got[i] != raw[i] {
				t.Fatalf("byte %d mismatch: got %d, want %d", i, got[i], raw[i])
			}
		}
	})

	t.Run("raw 32-byte string", func(t *testing.T) {
		raw := make([]byte, 32)
		for i := range raw {
			raw[i] = byte(255 - i)
		}
		got, err := MasterKey(func(s string) (string, bool) {
			if s == "MASTER_KEY" {
				return string(raw), true
			}
			return "", false
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 32 {
			t.Fatalf("expected 32 bytes, got %d", len(got))
		}
		for i := range raw {
			if got[i] != raw[i] {
				t.Fatalf("byte %d mismatch: got %d, want %d", i, got[i], raw[i])
			}
		}
	})

	t.Run("short value", func(t *testing.T) {
		_, err := MasterKey(func(s string) (string, bool) {
			if s == "MASTER_KEY" {
				return "tooshort", true
			}
			return "", false
		})
		if err == nil {
			t.Fatal("expected error for short key, got nil")
		}
	})

	t.Run("missing", func(t *testing.T) {
		_, err := MasterKey(func(s string) (string, bool) {
			return "", false
		})
		if err == nil {
			t.Fatal("expected error for missing key, got nil")
		}
	})
}

// --- Encrypt/Decrypt round-trip ---

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	lengths := []int{0, 1, 15, 16, 100, 1024}
	for _, n := range lengths {
		t.Run(fmt.Sprintf("len=%d", n), func(t *testing.T) {
			plaintext := make([]byte, n)
			rand.Read(plaintext)

			blob, err := Encrypt(key, plaintext)
			if err != nil {
				t.Fatalf("Encrypt error: %v", err)
			}
			// blob must be nonce(12) + ciphertext+tag (16 bytes tag for GCM)
			if len(blob) != 12+n+16 {
				t.Fatalf("blob length: got %d, want %d", len(blob), 12+n+16)
			}

			dec, err := Decrypt(key, blob)
			if err != nil {
				t.Fatalf("Decrypt error: %v", err)
			}
			if string(dec) != string(plaintext) {
				t.Fatalf("round-trip mismatch for len=%d", n)
			}
		})
	}
}

// --- Decrypt with wrong key ---

func TestDecryptWrongKey(t *testing.T) {
	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	keyB[0] = 1

	blob, err := Encrypt(keyA, []byte("secret data"))
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	_, err = Decrypt(keyB, blob)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key, got nil")
	}
}

// --- Decrypt corrupt/truncated blob ---

func TestDecryptCorrupt(t *testing.T) {
	key := make([]byte, 32)

	blob, err := Encrypt(key, []byte("hello"))
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	t.Run("truncated", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Decrypt panicked on truncated input: %v", r)
			}
		}()
		_, err := Decrypt(key, blob[:5])
		if err == nil {
			t.Fatal("expected error for truncated blob, got nil")
		}
	})

	t.Run("empty", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Decrypt panicked on empty input: %v", r)
			}
		}()
		_, err := Decrypt(key, []byte{})
		if err == nil {
			t.Fatal("expected error for empty blob, got nil")
		}
	})

	t.Run("garbled", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Decrypt panicked on garbled input: %v", r)
			}
		}()
		garbled := make([]byte, len(blob))
		copy(garbled, blob)
		garbled[len(garbled)-1] ^= 0xFF
		_, err := Decrypt(key, garbled)
		if err == nil {
			t.Fatal("expected error for garbled blob, got nil")
		}
	})
}

// --- SealJSON / OpenJSON ---

func TestSealOpenJSON(t *testing.T) {
	type testStruct struct {
		Token string
		N     int
	}

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	original := testStruct{Token: "sk-ant-abc123", N: 42}

	blob, err := SealJSON(key, original)
	if err != nil {
		t.Fatalf("SealJSON error: %v", err)
	}

	var decoded testStruct
	err = OpenJSON(key, blob, &decoded)
	if err != nil {
		t.Fatalf("OpenJSON error: %v", err)
	}

	if decoded.Token != original.Token {
		t.Fatalf("Token mismatch: got %q, want %q", decoded.Token, original.Token)
	}
	if decoded.N != original.N {
		t.Fatalf("N mismatch: got %d, want %d", decoded.N, original.N)
	}

	t.Run("wrong key", func(t *testing.T) {
		wrongKey := make([]byte, 32)
		wrongKey[0] = 99
		var dst testStruct
		err := OpenJSON(wrongKey, blob, &dst)
		if err == nil {
			t.Fatal("expected error with wrong key, got nil")
		}
	})

	t.Run("corrupt blob", func(t *testing.T) {
		corrupt := make([]byte, len(blob))
		copy(corrupt, blob)
		corrupt[0] ^= 0xFF
		var dst testStruct
		err := OpenJSON(key, corrupt, &dst)
		if err == nil {
			t.Fatal("expected error with corrupt blob, got nil")
		}
	})
}
