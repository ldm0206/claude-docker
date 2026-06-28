// Package secrets provides AES-256-GCM encryption/decryption and a MASTER_KEY
// loader for the claude-docker backend. All operations use stdlib only.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MasterKey reads the MASTER_KEY via the provided getter (typically os.LookupEnv).
// It accepts either a base64-encoded string that decodes to exactly 32 bytes,
// or a raw 32-byte string. Returns an error if the key is missing or not 32 bytes.
func MasterKey(get func(string) (string, bool)) ([]byte, error) {
	val, ok := get("MASTER_KEY")
	if !ok || val == "" {
		return nil, errors.New("secrets: MASTER_KEY not set")
	}

	// Try base64 decode first.
	if decoded, err := base64.StdEncoding.DecodeString(val); err == nil {
		if len(decoded) == 32 {
			return decoded, nil
		}
	}

	// Fall back to raw bytes.
	if len(val) == 32 {
		return []byte(val), nil
	}

	return nil, fmt.Errorf("secrets: MASTER_KEY must be 32 bytes (got %d bytes raw, and base64 decode did not yield 32 bytes)", len(val))
}

// Encrypt encrypts plaintext using AES-256-GCM with the given 32-byte key.
// Output format: nonce(12) || ciphertext+tag(16).
// The only error condition is if the key is not 16, 24, or 32 bytes
// (callers should always pass a 32-byte key from MasterKey).
func Encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: aes.NewCipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: cipher.NewGCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secrets: generate nonce: %w", err)
	}

	// Seal appends ciphertext+tag to dst; we prepend nonce manually.
	blob := gcm.Seal(nonce, nonce, plaintext, nil)
	return blob, nil
}

// Decrypt decrypts a blob produced by Encrypt. The blob must be at least
// 12 bytes (nonce) + 16 bytes (GCM tag). Returns an error (never panics)
// on wrong key, truncated input, or any authentication failure.
func Decrypt(key, blob []byte) ([]byte, error) {
	if len(blob) < 12 {
		return nil, errors.New("secrets: ciphertext too short")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: aes.NewCipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: cipher.NewGCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(blob) < nonceSize+gcm.Overhead() {
		return nil, errors.New("secrets: ciphertext too short for GCM overhead")
	}

	nonce := blob[:nonceSize]
	ciphertext := blob[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("secrets: decrypt: %w", err)
	}

	return plaintext, nil
}

// SealJSON marshals v to JSON and then encrypts the result with Encrypt.
func SealJSON(key []byte, v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("secrets: json.Marshal: %w", err)
	}
	return Encrypt(key, data)
}

// OpenJSON decrypts blob with Decrypt and then unmarshals the result into dst.
func OpenJSON(key, blob []byte, dst any) error {
	data, err := Decrypt(key, blob)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("secrets: json.Unmarshal: %w", err)
	}
	return nil
}
