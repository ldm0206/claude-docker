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

const SK_ANT_SRC = "\\bsk-ant-[A-Za-z0-9_\\-]{6,}";
// Case-insensitive Bearer, tolerate a colon or no space after "Bearer", and
// require the token portion to contain a digit/punct char (lookahead) so prose
// like "Bearer authorization scheme" is not over-redacted.
const BEARER_SRC = "\\bBearer[\\s:]*(?=[A-Za-z0-9_.\\-=]*[0-9_.\\-=])[A-Za-z0-9_.\\-=]{6,}";

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
  const skAntRe = new RegExp(SK_ANT_SRC, "g");
  const bearerRe = new RegExp(BEARER_SRC, "gi");
  s = s.replace(skAntRe, "[REDACTED]");
  s = s.replace(bearerRe, "Bearer [REDACTED]");
  return s;
}
