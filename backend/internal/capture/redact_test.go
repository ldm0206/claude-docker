package capture

import (
	"testing"
)

func TestRedactReplacesSecrets(t *testing.T) {
	result := Redact("my api key is sk-1234567890 and token is abcdef", []string{"sk-1234567890", "abcdef"})
	want := "my api key is [REDACTED] and token is [REDACTED]"
	if result != want {
		t.Errorf("Redact() = %q, want %q", result, want)
	}
}

func TestRedactSkipsShortSecrets(t *testing.T) {
	result := Redact("the key is abc and secret is secretvalue", []string{"abc", "secretvalue"})
	// "abc" is <4 chars, should be skipped
	want := "the key is abc and secret is [REDACTED]"
	if result != want {
		t.Errorf("Redact() = %q, want %q", result, want)
	}
}

func TestRedactNoMatch(t *testing.T) {
	result := Redact("hello world", []string{"notfound1234"})
	if result != "hello world" {
		t.Errorf("Redact() = %q, want %q", result, "hello world")
	}
}

func TestRedactEmptySecrets(t *testing.T) {
	result := Redact("hello world", []string{})
	if result != "hello world" {
		t.Errorf("Redact() = %q, want %q", result, "hello world")
	}
}

func TestRedactMultipleOccurrences(t *testing.T) {
	result := Redact("key=secret123 and also key=secret123 again", []string{"secret123"})
	want := "key=[REDACTED] and also key=[REDACTED] again"
	if result != want {
		t.Errorf("Redact() = %q, want %q", result, want)
	}
}

func TestRedactExactBoundary(t *testing.T) {
	// 4-char secret should be redacted
	result := Redact("pass=abcd end", []string{"abcd"})
	want := "pass=[REDACTED] end"
	if result != want {
		t.Errorf("Redact(4-char) = %q, want %q", result, want)
	}

	// 3-char secret should NOT be redacted
	result2 := Redact("pass=abc end", []string{"abc"})
	if result2 != "pass=abc end" {
		t.Errorf("Redact(3-char) = %q, want %q", result2, "pass=abc end")
	}
}

func TestRedactHeaders(t *testing.T) {
	headers := map[string]string{
		"Authorization": "Bearer sk-1234567890",
		"Content-Type":  "application/json",
		"X-Api-Key":     "sk-1234567890",
	}
	secrets := []string{"sk-1234567890"}

	result := RedactHeaders(headers, secrets)

	if result["Authorization"] != "Bearer [REDACTED]" {
		t.Errorf("Authorization = %q, want %q", result["Authorization"], "Bearer [REDACTED]")
	}
	if result["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q, want %q", result["Content-Type"], "application/json")
	}
	if result["X-Api-Key"] != "[REDACTED]" {
		t.Errorf("X-Api-Key = %q, want %q", result["X-Api-Key"], "[REDACTED]")
	}
}

func TestRedactHeadersNilInput(t *testing.T) {
	result := RedactHeaders(nil, []string{"secret1234"})
	if result != nil {
		t.Errorf("RedactHeaders(nil) = %v, want nil", result)
	}
}

func TestRedactHeadersEmptyInput(t *testing.T) {
	result := RedactHeaders(map[string]string{}, []string{"secret1234"})
	if len(result) != 0 {
		t.Errorf("RedactHeaders(empty) = %d entries, want 0", len(result))
	}
}
