export const REDACT_HEADER_KEYS = [
  "x-api-key",
  "authorization",
  "cookie",
  "set-cookie",
  "proxy-authorization",
  "anthropic-api-key",
  "anthropic-auth-token",
  "anthropic-organization-id",
];

const SK_ANT = /sk-ant-[A-Za-z0-9_\-]{6,}/g;
const BEARER = /Bearer\s+[A-Za-z0-9_\-\.=]{6,}/g;

export function redactHeaders(headers, knownSecrets = []) {
  const out = {};
  for (const [k, v] of Object.entries(headers || {})) {
    const lk = String(k).toLowerCase();
    if (REDACT_HEADER_KEYS.includes(lk) || lk.startsWith("anthropic-")) {
      out[k] = "[REDACTED]";
    } else if (knownSecrets.some((s) => s && String(v).includes(s))) {
      out[k] = "[REDACTED]";
    } else {
      out[k] = v;
    }
  }
  return out;
}

export function redactBody(body, knownSecrets = []) {
  let s = body == null ? "" : String(body);
  for (const secret of knownSecrets) {
    if (secret && secret.length >= 4) {
      s = s.split(secret).join("[REDACTED]");
    }
  }
  s = s.replace(SK_ANT, "[REDACTED]");
  s = s.replace(BEARER, "Bearer [REDACTED]");
  return s;
}
