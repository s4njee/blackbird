import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deliverAttention, attentionCount } from "../src/store/attention";
import { noticeRecords, resetNotifications } from "../src/store/notifications";
import { buildHash, parseHash } from "../src/lib/router";

beforeEach(() => {
  localStorage.clear();
  resetNotifications();
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
  resetNotifications();
});
describe("attention notices", () => {
  it("coalesces a burst and never re-notifies unchanged incidents, even after the toast window", () => {
    const summary = { instance: "burst", noticeSequence: 1, open: 1 };
    for (let i = 0; i < 100; i++) {
      deliverAttention(summary);
      vi.advanceTimersByTime(10000);
    }
    expect(noticeRecords()).toHaveLength(1);
    expect(noticeRecords()[0].count).toBe(1);
    expect(attentionCount()).toBe(1);
    deliverAttention({ ...summary, noticeSequence: 2 });
    expect(noticeRecords()).toHaveLength(2);
  });
  it("honors the persisted watermark and suppresses snoozed/acknowledged/resolved delivery", () => {
    localStorage.setItem(
      "blackbird.attention.notices.v1",
      JSON.stringify({ instance: "restored", sequence: 4 }),
    );
    deliverAttention({ instance: "restored", noticeSequence: 4, open: 10 });
    expect(noticeRecords()).toHaveLength(0);
    deliverAttention({ instance: "restored", noticeSequence: 5, open: 0 });
    expect(noticeRecords()).toHaveLength(0);
    deliverAttention({ instance: "new-store", noticeSequence: 1, open: 1 });
    expect(noticeRecords()).toHaveLength(1);
  });
  it("round trips the attention route", () => {
    expect(parseHash(buildHash({ route: "attention" })).route).toBe("attention");
  });
  it("waits for a durable save before advancing the delivery watermark", () => {
    const summary = { instance: "disk-failure", noticeSequence: 1, open: 1 };
    deliverAttention({ ...summary, error: "Save failed" });
    expect(noticeRecords()).toHaveLength(0);
    deliverAttention(summary);
    expect(noticeRecords()).toHaveLength(1);
  });
});
