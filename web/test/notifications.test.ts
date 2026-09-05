// @vitest-environment happy-dom
// Notification system tests (POL-8.3): queue cap + overflow, dedup
// coalescing (SHIP-1.3), durations/sticky errors, dismiss, actions, centre
// ring + unread, prefs persistence, and the browser-notification policy.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  activeNotices,
  browserPolicy,
  clearNotices,
  dismissNotice,
  DEDUP_WINDOW_MS,
  MAX_RECORDS,
  MAX_VISIBLE,
  noticePrefs,
  noticeRecords,
  notify,
  overflowCount,
  requestBrowserPermission,
  resetNotifications,
  runNoticeAction,
  setBrowserEnabled,
  setCentreOpen,
  centreOpen,
  setDefaultDurationMs,
  unreadCount,
  visibleNotices,
} from "../src/store/notifications.js";

beforeEach(() => {
  resetNotifications();
  const backing = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => (backing.has(key) ? backing.get(key)! : null),
    setItem: (key: string, value: string) => void backing.set(key, value),
    removeItem: (key: string) => void backing.delete(key),
    clear: () => backing.clear(),
  });
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  resetNotifications();
});

describe("notifications", () => {
  it("queues at most MAX_VISIBLE toasts with an overflow count", () => {
    expect(MAX_VISIBLE).toBe(3);
    for (let i = 0; i < 5; i++) notify(`msg-${i}`);
    expect(visibleNotices().length).toBe(3);
    expect(overflowCount()).toBe(2);
    expect(activeNotices().length).toBe(5);
  });

  it("coalesces duplicate kind+message pairs inside the window", () => {
    notify("retrying");
    notify("retrying");
    notify("other");
    expect(activeNotices().length).toBe(2);
    expect(activeNotices()[0].count).toBe(2);
    // Past the window (and past expiry) a repeat is a new entry.
    vi.advanceTimersByTime(DEDUP_WINDOW_MS + 1);
    notify("retrying");
    expect(activeNotices().length).toBe(1);
    expect(activeNotices()[0].count).toBe(1);
  });

  it("does not coalesce across kinds", () => {
    notify("x", { kind: "info" });
    notify("x", { kind: "error" });
    expect(activeNotices().length).toBe(2);
  });

  it("expires info toasts and persists errors until dismissed", () => {
    notify("bye");
    notify("boom", { kind: "error" });
    expect(activeNotices().length).toBe(2);
    vi.advanceTimersByTime(60_000);
    expect(activeNotices().map((n) => n.message)).toEqual(["boom"]);
    dismissNotice(activeNotices()[0].id);
    expect(activeNotices().length).toBe(0);
  });

  it("honours per-call durations and the configured default", () => {
    setDefaultDurationMs(1000);
    notify("short");
    notify("custom", { durationMs: 5000 });
    vi.advanceTimersByTime(1500);
    expect(activeNotices().map((n) => n.message)).toEqual(["custom"]);
    vi.advanceTimersByTime(4000);
    expect(activeNotices().length).toBe(0);
  });

  it("runs actions and dismisses their toast", () => {
    let ran = 0;
    const id = notify("label set", { action: { label: "Undo", run: () => ran++ } });
    runNoticeAction(id);
    expect(ran).toBe(1);
    expect(activeNotices().length).toBe(0);
    // The centre record stays for review.
    expect(noticeRecords().length).toBe(1);
  });

  it("caps the centre ring at 50 with unread tracking and clear", () => {
    expect(MAX_RECORDS).toBe(50);
    for (let i = 0; i < 60; i++) notify(`event-${i}`, { silent: true });
    expect(noticeRecords().length).toBe(50);
    expect(noticeRecords()[0].message).toBe("event-59");
    expect(unreadCount()).toBe(50);
    setCentreOpen(true);
    expect(centreOpen()).toBe(true);
    expect(unreadCount()).toBe(0);
    setCentreOpen(false);
    notify("later", { silent: true });
    expect(unreadCount()).toBe(1);
    clearNotices();
    expect(noticeRecords().length).toBe(0);
    expect(activeNotices().length).toBe(0);
  });

  it("persists prefs per browser", () => {
    setBrowserEnabled(true);
    setDefaultDurationMs(9000);
    expect(noticePrefs()).toEqual({ browserEnabled: true, defaultDurationMs: 9000 });
    expect(JSON.parse(localStorage.getItem("blackbird.notifications.v1")!).defaultDurationMs).toBe(
      9000,
    );
  });

  it("gates browser notifications on opt-in, permission, and hidden tab", () => {
    expect(browserPolicy({ enabled: true, permission: "granted", hidden: true })).toBe(true);
    expect(browserPolicy({ enabled: false, permission: "granted", hidden: true })).toBe(false);
    expect(browserPolicy({ enabled: true, permission: "denied", hidden: true })).toBe(false);
    expect(browserPolicy({ enabled: true, permission: "default", hidden: true })).toBe(false);
    expect(browserPolicy({ enabled: true, permission: "granted", hidden: false })).toBe(false);
    expect(browserPolicy({ enabled: true, permission: undefined, hidden: true })).toBe(false);
  });

  it("raises browser notifications only when hidden with permission", () => {
    const seen: Array<{ title: string; body: string }> = [];
    vi.stubGlobal(
      "Notification",
      class {
        static permission: NotificationPermission = "granted";
        static async requestPermission() {
          return "granted" as const;
        }
        constructor(title: string, init?: { body?: string }) {
          seen.push({ title, body: init?.body ?? "" });
        }
      },
    );
    const hidden = Object.getOwnPropertyDescriptor(document, "hidden");
    Object.defineProperty(document, "hidden", { value: true, configurable: true });
    try {
      setBrowserEnabled(true);
      notify("done", { silent: true, browser: { title: "Download complete", body: "x.iso" } });
      expect(seen).toEqual([{ title: "Download complete", body: "x.iso" }]);
      Object.defineProperty(document, "hidden", { value: false, configurable: true });
      notify("done", { silent: true, browser: { title: "Download complete", body: "x.iso" } });
      expect(seen.length).toBe(1);
    } finally {
      if (hidden) Object.defineProperty(document, "hidden", hidden);
    }
  });

  it("runs the permission flow on enable and reverts on denial", async () => {
    let permission: NotificationPermission = "default";
    vi.stubGlobal("Notification", {
      get permission() {
        return permission;
      },
      async requestPermission() {
        return permission;
      },
    });
    permission = "granted";
    await expect(requestBrowserPermission()).resolves.toBe("granted");
    expect(noticePrefs().browserEnabled).toBe(true);
    setBrowserEnabled(false);
    permission = "denied";
    await expect(requestBrowserPermission()).resolves.toBe("denied");
    expect(noticePrefs().browserEnabled).toBe(false);
  });
});
