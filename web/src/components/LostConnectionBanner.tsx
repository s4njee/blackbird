import { Show } from "solid-js";
import { connection, lastError } from "../store/session";

/**
 * Persistent bar shown above the toolbar while the daemon is unreachable.
 * Deliberately fire-and-forget: it disappears automatically on reconnect.
 */
export function LostConnectionBanner() {
  return (
    <Show when={connection() === "disconnected"}>
      <div class="lost-connection" title={lastError() || undefined}>
        Lost connection to rTorrent — retrying…
      </div>
    </Show>
  );
}