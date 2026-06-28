package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	pwTime    = 1
	pwMemory  = 64 * 1024
	pwThreads = 2
	pwKeyLen  = 32
)

func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, pwTime, pwMemory, pwThreads, pwKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		pwMemory, pwTime, pwThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func CheckPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t, p uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	if len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, uint8(p), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// decoyHash is a precomputed argon2id hash used to equalize timing when a
// login references a user that does not exist: we still run CheckPassword so
// the missing-user path takes the same ~argon2id time as the wrong-password
// path, defeating user-enumeration via response timing. It is NEVER used to
// authenticate anyone.
var decoyHash = func() string {
	h, _ := HashPassword("__decoy_never_valid__")
	return h
}()

// CheckPasswordDecoy runs an argon2id verify against a throwaway hash so the
// caller spends the same time as a real wrong-password check. Result discarded.
func CheckPasswordDecoy(password string) { _ = CheckPassword(password, decoyHash) }

var ErrPasswordChecked = errors.New("password checked")
