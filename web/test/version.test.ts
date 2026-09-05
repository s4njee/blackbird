// @vitest-environment happy-dom
// Version store (POL-8.7): /api/version mapping, failure/retry, semver
// comparison, and the opt-in release check (never fetched unless enabled).
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  checkForUpdates,
  isNewerVersion,
  prefs,
  refreshVersion,
  release,
  resetReleaseForTests,
  setUpdateCheckEnabled,
  setUpdateCheckEndpoint,
  version,
  versionFailed,
  versionLoading,
} from "../src/store/version.js";

const ok = (body: unknown = {}) => ({ ok: true, json: async () => body });
const fail = () => ({ ok: false, status: 500, json: async () => ({}) });

function stubFetch(handler: (url: string) => unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: unknown) => handler(String(url))),
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("version store", () => {
  it("loads versions and degrades with a retry flag on failure", async () => {
    stubFetch(() => ok({ blackbird: { version: "1.0.0" }, rtorrent: { version: "0.16" } }));
    await refreshVersion();
    expect(version()?.blackbird.version).toBe("1.0.0");
    expect(versionFailed()).toBe(false);
    expect(versionLoading()).toBe(false);

    stubFetch(() => fail());
    await refreshVersion();
    expect(versionFailed()).toBe(true);
    // Previous data retained so the panel degrades, not blanks.
    expect(version()?.blackbird.version).toBe("1.0.0");

    stubFetch(() => ok({ blackbird: { version: "1.0.1" }, rtorrent: {} }));
    await refreshVersion();
    expect(versionFailed()).toBe(false);
    expect(version()?.blackbird.version).toBe("1.0.1");
  });

  it("compares versions for the update check", () => {
    expect(isNewerVersion("1.0.0", "1.0.1")).toBe(true);
    expect(isNewerVersion("1.0.1", "1.0.0")).toBe(false);
    expect(isNewerVersion("1.0.0", "1.0.0")).toBe(false);
    expect(isNewerVersion("v1.2.3", "v1.10.0")).toBe(true);
    expect(isNewerVersion("1.9.0", "1.10.0")).toBe(true);
  });

  it("never checks for updates unless opted in with an endpoint", async () => {
    setUpdateCheckEnabled(false);
    setUpdateCheckEndpoint("https://example.invalid/releases/latest");
    resetReleaseForTests();
    const spy = vi.fn(async () => ok({ tag_name: "v9.9.9" }));
    await checkForUpdates(spy as unknown as typeof fetch);
    expect(spy).not.toHaveBeenCalled();
    expect(release()).toEqual({ status: "idle" });

    setUpdateCheckEnabled(true);
    setUpdateCheckEndpoint("");
    await checkForUpdates(spy as unknown as typeof fetch);
    expect(spy).not.toHaveBeenCalled();

    expect(prefs().enabled).toBe(true);
    setUpdateCheckEnabled(false);
    expect(prefs().enabled).toBe(false);
  });

  it("reports current vs update vs failure from the release endpoint", async () => {
    setUpdateCheckEnabled(true);
    setUpdateCheckEndpoint("https://example.invalid/releases/latest");
    stubFetch(() => ok({ blackbird: { version: "1.0.0" } }));
    await refreshVersion();

    await checkForUpdates(async () => ok({ tag_name: "v1.0.0", html_url: "https://x/y" }));
    expect(release().status).toBe("current");

    await checkForUpdates(async () => ok({ tag_name: "v1.2.0", html_url: "https://x/y" }));
    const state = release();
    expect(state.status).toBe("update");
    if (state.status === "update") expect(state.latest).toBe("v1.2.0");

    await checkForUpdates(async () => fail());
    expect(release().status).toBe("failed");

    setUpdateCheckEnabled(false);
    setUpdateCheckEndpoint("");
  });
});
