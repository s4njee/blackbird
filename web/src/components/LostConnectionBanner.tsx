import { Show } from "solid-js";
import { connection, lastError } from "../store/session";

/**
 * Persistent bar shown above the toolbar while the daemon is unreachable.
 * A live region so assistive tech announces disconnects and recovery.
 */
export function LostConnectionBanner() {
  return (
    <Show when={connection() === "disconnected"}>
      <div class="lost-connection" role="alert" title={lastError() || undefined}>
        Lost connection to rTorrent — retrying…
      </div>
    </Show>
  );
}
