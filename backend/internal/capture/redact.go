package capture

import "strings"

// Redact replaces every occurrence of each secret (length >= 4) in s with
// "[REDACTED]". Secrets shorter than 4 characters are skipped to avoid
// clobbering common short substrings. Matching is case-sensitive and
// best-effort exact substring.
func Redact(s string, secrets []string) string {
	for _, secret := range secrets {
		if len(secret) < 4 {
			continue
		}
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	return s
}

// RedactHeaders returns a copy of h with each value redacted using Redact.
// Returns nil if h is nil.
func RedactHeaders(h map[string]string, secrets []string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = Redact(v, secrets)
	}
	return out
}
