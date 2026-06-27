package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

func EqualString(a, b string) bool {
	ab, bb := []byte(a), []byte(b)
	if len(ab) != len(bb) {
		subtle.ConstantTimeCompare(ab, ab) // constant-time-ish regardless of length
		return false
	}
	return subtle.ConstantTimeCompare(ab, bb) == 1
}

func SignSession(payload map[string]any, secret string) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	b64 := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(b64))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return b64 + "." + sig, nil
}

func VerifySession(cookie, secret string) (map[string]any, bool) {
	if cookie == "" {
		return nil, false
	}
	b64, sig, ok := splitOnce(cookie, ".")
	if !ok {
		return nil, false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(b64))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || len(got) != len(want) {
		return nil, false
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return nil, false
	}
	body, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, false
	}
	return out, true
}

func splitOnce(s, sep string) (a, b string, ok bool) {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return "", "", false
}
