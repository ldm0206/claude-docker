import { describe, it, expect } from "vitest";
import { createCaptureStore } from "../src/capture-store.js";

const rec = (n) => ({
  ts: n, method: "POST", host: "api.anthropic.com", path: "/v1/messages",
  status: 200, latencyMs: n,
  reqHeaders: { "x-api-key": "sk-ant-abc" }, reqBody: '{"model":"x"}',
  resHeaders: { "content-type": "application/json" }, resBody: '{"id":"m"}',
});

describe("capture-store", () => {
  it("stores redacted records and lists newest first", () => {
    const s = createCaptureStore({ max: 3 });
    s.add(rec(1)); s.add(rec(2));
    const list = s.list();
    expect(list).toHaveLength(2);
    expect(list[0].latencyMs).toBe(2);
    expect(list[0].reqHeaders["x-api-key"]).toBe("[REDACTED]");
    expect(list[0].id).toBeTruthy();
  });

  it("caps to max and evicts oldest", () => {
    const s = createCaptureStore({ max: 2 });
    s.add(rec(1)); s.add(rec(2)); s.add(rec(3));
    expect(s.list()).toHaveLength(2);
    expect(s.list().map((r) => r.latencyMs)).toEqual([3, 2]);
  });

  it("clear empties the store", () => {
    const s = createCaptureStore();
    s.add(rec(1));
    s.clear();
    expect(s.list()).toHaveLength(0);
  });

  it("notifies subscribers with redacted records", () => {
    const s = createCaptureStore();
    const got = [];
    const unsub = s.subscribe((r) => got.push(r));
    s.add(rec(1));
    expect(got).toHaveLength(1);
    expect(got[0].reqHeaders["x-api-key"]).toBe("[REDACTED]");
    unsub();
    s.add(rec(2));
    expect(got).toHaveLength(1);
  });
});
