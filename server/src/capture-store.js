import { redactBody, redactHeaders } from "./redact.js";

let counter = 0;

export function createCaptureStore({ max = 500, knownSecrets = [] } = {}) {
  const records = [];
  const subs = new Set();

  function redact(rec) {
    return {
      ...rec,
      reqHeaders: redactHeaders(rec.reqHeaders || {}, knownSecrets),
      resHeaders: redactHeaders(rec.resHeaders || {}, knownSecrets),
      reqBody: redactBody(rec.reqBody, knownSecrets),
      resBody: redactBody(rec.resBody, knownSecrets),
    };
  }

  return {
    add(rec) {
      const stored = { id: ++counter, ...redact(rec) };
      records.unshift(stored);
      while (records.length > max) records.pop();
      for (const fn of subs) fn(stored);
      return stored;
    },
    list() {
      return records.slice();
    },
    clear() {
      records.length = 0;
    },
    subscribe(fn) { subs.add(fn); return () => subs.delete(fn); },
  };
}
