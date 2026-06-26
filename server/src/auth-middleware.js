import { verifySession } from "./auth.js";

export function requireAuth(secret) {
  return async function (req, reply) {
    const cookie = req.cookies?.session;
    const payload = verifySession(cookie, secret);
    if (!payload) {
      reply.code(401).send({ error: "unauthorized" });
      return;
    }
    req.session = payload;
  };
}
