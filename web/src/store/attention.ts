import { createEffect, createSignal, onCleanup, untrack } from "solid-js";
import { notify } from "./notifications";
import { isTabHidden, tickerTick } from "./ticker";
import { navigate } from "./ui";

export const [attentionCount, setAttentionCount] = createSignal(0);
const KEY = "blackbird.attention.notices.v1";
let memory = { instance: "", sequence: 0 };

/** Durable browser watermark: membership/last-seen changes never re-toast.
 * Each browser receives at most one aggregate notice per attention transition. */
export function deliverAttention(summary: {
  instance: string;
  noticeSequence: number;
  open: number;
  error?: string;
}) {
  if (
    !summary.instance ||
    !Number.isSafeInteger(summary.noticeSequence) ||
    !Number.isSafeInteger(summary.open) ||
    summary.open < 0
  )
    return;
  setAttentionCount(summary.open);
  // Only advance the durable delivery watermark for successfully saved
  // transitions; a failed save could otherwise reuse this sequence on restart.
  if (summary.error) return;
  try {
    const saved = JSON.parse(localStorage.getItem(KEY) || "null");
    if (saved?.instance === summary.instance && Number.isSafeInteger(saved.sequence))
      memory = {
        instance: saved.instance,
        sequence: Math.max(
          memory.instance === saved.instance ? memory.sequence : 0,
          saved.sequence,
        ),
      };
  } catch {
    /* The in-memory watermark still suppresses repeated delivery. */
  }
  if (memory.instance === summary.instance && memory.sequence >= summary.noticeSequence) return;
  memory = { instance: summary.instance, sequence: summary.noticeSequence };
  try {
    localStorage.setItem(KEY, JSON.stringify(memory));
  } catch {
    /* Session fallback. */
  }
  if (summary.open > 0)
    notify(`${summary.open} incident${summary.open === 1 ? " needs" : "s need"} attention.`, {
      kind: "warning",
      action: { label: "Open inbox", run: () => navigate("attention") },
    });
}

export function monitorAttention() {
  let request: AbortController | undefined;
  let bucket = -1;
  createEffect(() => {
    const hidden = isTabHidden();
    const next = Math.floor(tickerTick() / 10);
    if (hidden) {
      request?.abort();
      bucket = -1;
      return;
    }
    if (next === bucket) return;
    bucket = next;
    untrack(() => {
      request?.abort();
      const active = new AbortController();
      request = active;
      void fetch("/api/v1/attention?summary=1", {
        signal: active.signal,
        headers: { Accept: "application/json" },
      })
        .then(async (response) => {
          if (response.ok) {
            const body = await response.json();
            if (!active.signal.aborted) deliverAttention(body);
          }
        })
        .catch(() => {
          /* Inbox shows availability and storage errors on open. */
        });
    });
  });
  onCleanup(() => request?.abort());
}
