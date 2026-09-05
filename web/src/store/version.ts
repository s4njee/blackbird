// Version store (POL-8.7): the Settings > About panel reads /api/version for
// the Blackbird build plus live rTorrent/libtorrent versions. The optional
// release check is browser-local and opt-in: nothing is fetched until the
// user enables it and picks an endpoint, so fresh installs make no external
// requests.

import { createSignal } from "solid-js";

export type VersionInfo = {
  blackbird: { version: string; commit: string; buildDate: string };
  rtorrent: { version: string; library: string };
  connection: string;
  torrents: number;
};

export type ReleaseState =
  | { status: "idle" }
  | { status: "checking" }
  | { status: "current"; latest: string; checkedAt: string }
  | { status: "update"; latest: string; checkedAt: string; url: string }
  | { status: "failed"; message: string };

const [version, setVersion] = createSignal<VersionInfo | null>(null);
const [versionLoading, setVersionLoading] = createSignal(true);
const [versionFailed, setVersionFailed] = createSignal(false);

export { version, versionLoading, versionFailed };

export async function refreshVersion(): Promise<void> {
  setVersionLoading(true);
  setVersionFailed(false);
  try {
    const response = await fetch("/api/v1/version", { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    setVersion((await response.json()) as VersionInfo);
  } catch {
    setVersionFailed(true);
  } finally {
    setVersionLoading(false);
  }
}

const CHECK_PREFS_KEY = "blackbird.update-check.v1";

export type UpdateCheckPrefs = { enabled: boolean; endpoint: string };

function hasLocalStorage() {
  return typeof window !== "undefined" && typeof window.localStorage !== "undefined";
}

function loadPrefs(): UpdateCheckPrefs {
  if (!hasLocalStorage()) return { enabled: false, endpoint: "" };
  try {
    const raw = window.localStorage.getItem(CHECK_PREFS_KEY);
    if (!raw) return { enabled: false, endpoint: "" };
    const parsed = JSON.parse(raw) as Partial<UpdateCheckPrefs>;
    return {
      enabled: parsed.enabled === true,
      endpoint: typeof parsed.endpoint === "string" ? parsed.endpoint : "",
    };
  } catch {
    return { enabled: false, endpoint: "" };
  }
}

const [prefs, setPrefs] = createSignal<UpdateCheckPrefs>(loadPrefs());
const [release, setRelease] = createSignal<ReleaseState>({ status: "idle" });

export { prefs, release };

function persistPrefs(next: UpdateCheckPrefs) {
  setPrefs(next);
  if (hasLocalStorage()) {
    try {
      window.localStorage.setItem(CHECK_PREFS_KEY, JSON.stringify(next));
    } catch {
      /* private mode: prefs stay in memory */
    }
  }
}

export function setUpdateCheckEnabled(enabled: boolean) {
  persistPrefs({ ...prefs(), enabled });
  if (!enabled) setRelease({ status: "idle" });
}

export function setUpdateCheckEndpoint(endpoint: string) {
  persistPrefs({ ...prefs(), endpoint });
}

/** Compares dotted versions; returns true when latest is newer than current. */
export function isNewerVersion(current: string, latest: string): boolean {
  const norm = (v: string) =>
    v
      .replace(/^[vV]/, "")
      .split(/[.+_-]/)
      .map((p) => p.trim());
  const a = norm(current);
  const b = norm(latest);
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    const x = a[i] ?? "";
    const y = b[i] ?? "";
    if (x === y) continue;
    const nx = Number(x);
    const ny = Number(y);
    if (Number.isInteger(nx) && Number.isInteger(ny) && String(nx) === x && String(ny) === y) {
      return ny > nx;
    }
    return y > x;
  }
  return false;
}

type ReleasePayload = { tag_name?: string; name?: string; html_url?: string };

export async function checkForUpdates(fetchImpl: typeof fetch = fetch): Promise<void> {
  const { enabled, endpoint } = prefs();
  if (!enabled || !endpoint.trim()) return;
  const current = version()?.blackbird.version ?? "";
  setRelease({ status: "checking" });
  try {
    const response = await fetchImpl(endpoint.trim(), { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const body = (await response.json()) as ReleasePayload;
    const latest = (body.tag_name || body.name || "").trim();
    if (!latest) throw new Error("Release endpoint returned no version");
    const checkedAt = new Date().toISOString();
    if (current && current !== "dev" && isNewerVersion(current, latest)) {
      setRelease({ status: "update", latest, checkedAt, url: body.html_url ?? endpoint.trim() });
    } else {
      setRelease({ status: "current", latest, checkedAt });
    }
  } catch (error) {
    setRelease({
      status: "failed",
      message: error instanceof Error ? error.message : "Update check failed",
    });
  }
}

/** Test-only reset for the release state. */
export function resetReleaseForTests() {
  setRelease({ status: "idle" });
}
