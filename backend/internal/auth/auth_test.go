package auth

import "testing"

func TestEqualString(t *testing.T) {
	if !EqualString("abc", "abc") {
		t.Fatal("equal strings should match")
	}
	if EqualString("abc", "abd") {
		t.Fatal("different strings should not match")
	}
	if EqualString("a", "ab") {
		t.Fatal("different-length strings should not match")
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	s, err := SignSession(map[string]any{"iat": int64(123)}, "secret")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	out, ok := VerifySession(s, "secret")
	// json.Unmarshal into map[string]any decodes numbers as float64, so the
	// round-trip yields float64(123) — assert the value is preserved.
	if !ok || out["iat"] != float64(123) {
		t.Fatalf("verify failed: %v %v", out, ok)
	}
	if _, ok := VerifySession(s, "wrong"); ok {
		t.Fatal("should reject wrong secret")
	}
	if _, ok := VerifySession("garbage", "secret"); ok {
		t.Fatal("should reject malformed cookie")
	}
}
