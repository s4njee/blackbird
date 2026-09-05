// @vitest-environment happy-dom
// Fetch-backed stores (POL-8.1): refresh/action paths with stubbed fetch —
// success mapping, silent-refresh failure retention, and error surfacing.
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  applyBandwidth,
  bandwidth,
  refreshBandwidth,
  saveBandwidthDefault,
} from "../src/store/bandwidth.js";
import { channels, refreshThrottles } from "../src/store/throttles.js";
import {
  checking,
  enabled,
  refreshPortCheck,
  runPortCheck,
  verdict,
} from "../src/store/portcheck.js";
import {
  clearScheduleOverride,
  refreshSchedule,
  scheduleStatus,
  setScheduleOverride,
} from "../src/store/schedule.js";
import { policy, refreshSeeding } from "../src/store/seeding.js";
import { refreshIPFilter, reloading, reloadIPFilter, status } from "../src/store/ipfilter.js";
import {
  addRssItem,
  loading,
  markRssRead,
  refreshRss,
  unreadCount,
  view,
} from "../src/store/rss.js";

type Handler = (url: string, init?: RequestInit) => unknown;

const ok = (body: unknown = {}) => ({ ok: true, json: async () => body });
const fail = () => ({ ok: false, status: 500, json: async () => ({}) });

function stubFetch(handler: Handler) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: unknown, init?: RequestInit) => handler(String(url), init)),
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("fetch stores", () => {
  it("refreshes throttle channels and retains the list on failure", async () => {
    stubFetch(() => ok({ channels: [{ name: "slow" }] }));
    await refreshThrottles();
    expect(channels().map((c) => c.name)).toEqual(["slow"]);
    stubFetch(() => fail());
    await refreshThrottles();
    expect(channels().map((c) => c.name)).toEqual(["slow"]);
    stubFetch(() => {
      throw new Error("down");
    });
    await refreshThrottles();
    expect(channels().map((c) => c.name)).toEqual(["slow"]);
  });

  it("maps bandwidth limits with zero fallbacks and posts actions", async () => {
    stubFetch(() => ok({ downKb: "100", upKb: 50 }));
    await refreshBandwidth();
    expect(bandwidth().downKb).toBe(100);
    expect(bandwidth().upKb).toBe(50);
    stubFetch(() => fail());
    await refreshBandwidth();
    expect(bandwidth().downKb).toBe(100);

    const calls: Array<{ url: string; init?: RequestInit }> = [];
    stubFetch((url, init) => {
      calls.push({ url, init });
      return ok({ downKb: 10, upKb: 20 });
    });
    await applyBandwidth(10, 20);
    expect(calls[0].url).toBe("/api/v1/bandwidth");
    expect(calls[0].init?.method).toBe("POST");
    expect(bandwidth().downKb).toBe(10);

    stubFetch(() => fail());
    await expect(applyBandwidth(1, 1)).rejects.toThrow();
  });

  it("saves bandwidth defaults through the settings round-trip", async () => {
    stubFetch((url) => {
      if (url === "/api/v1/bandwidth") return ok({});
      return ok({ tuning: { a: 1 }, saved: true });
    });
    await saveBandwidthDefault(5, 6);
    stubFetch((url) => (url === "/api/v1/bandwidth" ? ok({}) : fail()));
    await expect(saveBandwidthDefault(5, 6)).rejects.toThrow("Could not load settings");
    stubFetch((url) => {
      if (url === "/api/v1/bandwidth") return ok({});
      return ok({ tuning: {}, saved: false });
    });
    await expect(saveBandwidthDefault(5, 6)).rejects.toThrow();
  });

  it("tracks port-check verdicts and the checking flag", async () => {
    stubFetch(() => ok({ enabled: true, result: { port: 51413, reachable: true } }));
    await refreshPortCheck();
    expect(enabled()).toBe(true);
    expect(verdict()?.port).toBe(51413);
    stubFetch(() => ok({ result: { port: 51413, reachable: false } }));
    await runPortCheck();
    expect(checking()).toBe(false);
    expect(verdict()?.reachable).toBe(false);
    stubFetch(() => fail());
    await runPortCheck();
    expect(checking()).toBe(false);
    expect(verdict()?.reachable).toBe(false);
  });

  it("refreshes schedule status and applies overrides", async () => {
    stubFetch(() => ok({ activeProfile: "night", overridden: false }));
    await refreshSchedule();
    expect(scheduleStatus()?.activeProfile).toBe("night");
    const calls: string[] = [];
    stubFetch((url) => {
      calls.push(url);
      return url === "/api/v1/schedule/override" ? ok({}) : ok({ activeProfile: "day" });
    });
    await setScheduleOverride(30, 100, 50);
    expect(scheduleStatus()?.activeProfile).toBe("day");
    stubFetch(() => fail());
    await expect(setScheduleOverride(30, 1, 1)).rejects.toThrow();
    stubFetch(() => ok({ activeProfile: "off" }));
    await clearScheduleOverride();
    expect(scheduleStatus()?.activeProfile).toBe("off");
  });

  it("normalizes seeding policy groups and keeps defaults on failure", async () => {
    stubFetch(() => ok({ customSlot: "", groups: [{ name: "a" }, { name: "" }, {}] }));
    await refreshSeeding();
    expect(policy()).toEqual({ customSlot: "custom2", groups: [{ name: "a" }] });
    stubFetch(() => fail());
    await refreshSeeding();
    expect(policy().groups).toEqual([{ name: "a" }]);
  });

  it("reloads the blocklist and resets the reloading flag", async () => {
    stubFetch(() => ok({ enabled: true, source: "file", rules: 10 }));
    await refreshIPFilter();
    expect(status()?.rules).toBe(10);
    stubFetch(() => ok({ enabled: true, source: "file", rules: 42 }));
    await reloadIPFilter();
    expect(reloading()).toBe(false);
    expect(status()?.rules).toBe(42);
    stubFetch((url) => (url === "/api/v1/ipfilter/reload" ? fail() : ok({ rules: 7 })));
    await reloadIPFilter();
    expect(reloading()).toBe(false);
    expect(status()?.rules).toBe(7);
  });

  it("loads RSS views, counts unread, and runs item actions", async () => {
    const payload = {
      feeds: [{ name: "f", unread: 3 }],
      items: [],
      filters: [],
    };
    stubFetch(() => ok(payload));
    await refreshRss();
    expect(loading()).toBe(false);
    expect(view().feeds.length).toBe(1);
    expect(unreadCount()).toBe(3);
    stubFetch(() => fail());
    await refreshRss(true);
    expect(loading()).toBe(false);
    expect(unreadCount()).toBe(3);
    stubFetch(() => ok({ hash: "abc" }));
    await addRssItem("f", "1");
    await markRssRead("f");
    stubFetch(() => fail());
    await addRssItem("f", "1");
    await markRssRead();
  });
});
