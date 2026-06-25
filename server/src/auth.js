import crypto from "node:crypto";

export function timingSafeEqualStr(a, b) {
  const ab = Buffer.from(String(a));
  const bb = Buffer.from(String(b));
  if (ab.length !== bb.length) {
    crypto.timingSafeEqual(ab, ab); // constant-time-ish regardless of length
    return false;
  }
  return crypto.timingSafeEqual(ab, bb);
}

export function signSession(payload, secret) {
  const b64 = Buffer.from(JSON.stringify(payload)).toString("base64url");
  const mac = crypto.createHmac("sha256", secret).update(b64).digest("base64url");
  return `${b64}.${mac}`;
}

export function verifySession(cookie, secret) {
  if (typeof cookie !== "string" || !cookie.includes(".")) return null;
  const [b64, mac] = cookie.split(".");
  if (!b64 || !mac) return null;
  const expected = crypto.createHmac("sha256", secret).update(b64).digest("base64url");
  const ok = Buffer.from(mac).length === Buffer.from(expected).length
    && crypto.timingSafeEqual(Buffer.from(mac), Buffer.from(expected));
  if (!ok) return null;
  try {
    return JSON.parse(Buffer.from(b64, "base64url").toString("utf8"));
  } catch {
    return null;
  }
}
