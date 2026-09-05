// Notification system (POL-8.3): one queued, severity-aware record used by
// toasts and the notification centre alike. `showToast` (store/ui.ts) stays
// the call-site API and delegates here, so existing callers are untouched.
//
// Rules: at most MAX_VISIBLE toasts on screen with an overflow count;
// duplicate kind+message pairs within DEDUP_WINDOW_MS coalesce into a count
// (shared with backlog.md SHIP-1.3); errors persist until dismissed while
// other kinds expire (configurable default); the centre keeps the last 50
// records with timestamps. Browser Notifications fire only when enabled in
// Settings, permitted, and the tab is hidden.

import { batch, createMemo, createSignal } from "solid-js";

export type NoticeKind = "success" | "info" | "warning" | "error";

/** Optional action button on a toast/centre row (Undo, Retry, View, …). */
export type NoticeAction = {
  label: string;
  run: () => void;
};

export type Notice = {
  id: number;
  kind: NoticeKind;
  message: string;
  /** Coalesced duplicate count (SHIP-1.3); 1 means a single event. */
  count: number;
  at: number;
  action?: NoticeAction;
  /** Errors ignore expiry until dismissed. */
  sticky: boolean;
};

/** Max toasts on screen; the rest collapse into the overflow count. */
export const MAX_VISIBLE = 3;

/** Centre ring capacity. */
export const MAX_RECORDS = 50;

/** Duplicates inside this window coalesce instead of queuing. */
export const DEDUP_WINDOW_MS = 5000;

const KIND_DURATION_MS: Record<Exclude<NoticeKind, "error">, number> = {
  success: 4200,
  info: 4200,
  warning: 6000,
};

const PREFS_KEY = "blackbird.notifications.v1";
const DEFAULT_DURATION_MS = 4200;

export type NoticePrefs = {
  browserEnabled: boolean;
  defaultDurationMs: number;
};

function loadPrefs(): NoticePrefs {
  const fallback: NoticePrefs = { browserEnabled: false, defaultDurationMs: DEFAULT_DURATION_MS };
  try {
    const raw = localStorage.getItem(PREFS_KEY);
    if (!raw) return fallback;
    const parsed = JSON.parse(raw) as Partial<NoticePrefs>;
    return {
      browserEnabled: parsed.browserEnabled === true,
      defaultDurationMs:
        typeof parsed.defaultDurationMs === "number" && parsed.defaultDurationMs >= 0
          ? Math.round(parsed.defaultDurationMs)
          : DEFAULT_DURATION_MS,
    };
  } catch {
    return fallback;
  }
}

const [prefs, setPrefs] = createSignal<NoticePrefs>(loadPrefs());

function persistPrefs(next: NoticePrefs) {
  setPrefs(next);
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify(next));
  } catch {
    /* private-mode quota: prefs stay in memory for the session */
  }
}

/** Browser-notification opt-in (Settings toggle). */
export function setBrowserEnabled(enabled: boolean) {
  persistPrefs({ ...prefs(), browserEnabled: enabled });
}

/** Default toast lifetime for non-error kinds (Settings, ms; 0 disables auto-dismiss). */
export function setDefaultDurationMs(ms: number) {
  persistPrefs({ ...prefs(), defaultDurationMs: Math.max(0, Math.round(ms)) });
}

export { prefs as noticePrefs };

let nextId = 1;
const [active, setActive] = createSignal<Notice[]>([]);
const [records, setRecords] = createSignal<Notice[]>([]);
const [centreOpen, setCentreOpenSignal] = createSignal(false);
const [lastSeenId, setLastSeenId] = createSignal(0);
const timers = new Map<number, ReturnType<typeof setTimeout>>();

export { active as activeNotices, records as noticeRecords, centreOpen };

export function setCentreOpen(open: boolean) {
  setCentreOpenSignal(open);
  if (open) {
    const list = records();
    if (list.length) setLastSeenId(list[0].id);
  }
}

/** Visible toasts (at most MAX_VISIBLE, oldest first). */
export const visibleNotices = createMemo(() => active().slice(0, MAX_VISIBLE));

/** Toasts waiting behind the visible window. */
export const overflowCount = createMemo(() => Math.max(0, active().length - MAX_VISIBLE));

/** Unread centre entries since last open. */
export const unreadCount = createMemo(() => records().filter((n) => n.id > lastSeenId()).length);

function clearTimer(id: number) {
  const timer = timers.get(id);
  if (timer !== undefined) {
    clearTimeout(timer);
    timers.delete(id);
  }
}

function lifetimeMs(kind: NoticeKind, durationMs?: number): number | null {
  if (kind === "error") return null;
  if (durationMs !== undefined) return durationMs > 0 ? durationMs : null;
  const configured = prefs().defaultDurationMs;
  if (configured === 0) return null;
  return configured > 0 ? configured : (KIND_DURATION_MS[kind] ?? DEFAULT_DURATION_MS);
}

export function notify(
  message: string,
  options: {
    kind?: NoticeKind;
    durationMs?: number;
    sticky?: boolean;
    action?: NoticeAction;
    /** Record in the centre without showing a toast. */
    silent?: boolean;
    /** Also raise a browser Notification (policy-gated). */
    browser?: { title: string; body?: string };
  } = {},
): number {
  const kind = options.kind ?? "info";
  const now = Date.now();
  const current = active();
  const dupe = current.find(
    (n) => n.kind === kind && n.message === message && now - n.at < DEDUP_WINDOW_MS,
  );
  if (dupe) {
    const bumped = { ...dupe, count: dupe.count + 1 };
    batch(() => {
      setActive(current.map((n) => (n.id === dupe.id ? bumped : n)));
      setRecords((prev) => prev.map((n) => (n.id === dupe.id ? bumped : n)));
    });
    if (options.browser) browserNotify(options.browser.title, options.browser.body ?? message);
    return dupe.id;
  }
  const id = nextId++;
  const sticky = options.sticky ?? kind === "error";
  const notice: Notice = {
    id,
    kind,
    message,
    count: 1,
    at: now,
    action: options.action,
    sticky,
  };
  batch(() => {
    if (!options.silent) setActive([...current, notice]);
    setRecords((prev) => [notice, ...prev].slice(0, MAX_RECORDS));
  });
  if (!sticky && !options.silent) {
    const ttl = lifetimeMs(kind, options.durationMs);
    if (ttl !== null) {
      timers.set(
        id,
        setTimeout(() => {
          timers.delete(id);
          setActive((prev) => prev.filter((n) => n.id !== id));
        }, ttl),
      );
    }
  }
  if (options.browser) browserNotify(options.browser.title, options.browser.body ?? message);
  return id;
}

/** Dismisses one toast (its centre record stays). */
export function dismissNotice(id: number) {
  clearTimer(id);
  setActive((prev) => prev.filter((n) => n.id !== id));
}

/** Runs a notice action, then dismisses its toast. */
export function runNoticeAction(id: number) {
  const found = active().find((n) => n.id === id) ?? records().find((n) => n.id === id);
  dismissNotice(id);
  found?.action?.run();
}

/** Clears toasts and the centre ring. */
export function clearNotices() {
  for (const id of timers.keys()) clearTimer(id);
  batch(() => {
    setActive([]);
    setRecords([]);
  });
}

/** Test hook: resets all notification state. */
export function resetNotifications() {
  for (const id of timers.keys()) clearTimer(id);
  batch(() => {
    setActive([]);
    setRecords([]);
    setLastSeenId(0);
    setCentreOpen(false);
  });
  nextId = 1;
}

// ---- Browser Notifications (opt-in, hidden-tab only) ----

/** Whether a browser Notification may fire right now (pure policy core). */
export function browserPolicy(args: {
  enabled: boolean;
  permission: NotificationPermission | undefined;
  hidden: boolean;
}): boolean {
  return args.enabled && args.permission === "granted" && args.hidden;
}

function browserPermission(): NotificationPermission | undefined {
  return typeof Notification === "undefined" ? undefined : Notification.permission;
}

function browserNotify(title: string, body: string) {
  if (typeof Notification === "undefined" || typeof document === "undefined") return;
  if (
    !browserPolicy({
      enabled: prefs().browserEnabled,
      permission: browserPermission(),
      hidden: document.hidden,
    })
  ) {
    return;
  }
  try {
    new Notification(title, { body, tag: `blackbird-${title}` });
  } catch {
    /* denied between check and raise: stay silent */
  }
}

/** Settings permission flow: requests on enable, reverts on denial. */
export async function requestBrowserPermission(): Promise<NotificationPermission | undefined> {
  if (typeof Notification === "undefined") return undefined;
  if (Notification.permission === "granted") {
    setBrowserEnabled(true);
    return "granted";
  }
  try {
    const result = await Notification.requestPermission();
    setBrowserEnabled(result === "granted");
    return result;
  } catch {
    setBrowserEnabled(false);
    return Notification.permission;
  }
}
