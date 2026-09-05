import { For, Show, onMount } from "solid-js";
import { helpOpen, setHelpOpen } from "../store/ui";
import { SHORTCUTS } from "../lib/shortcuts";

/** Shortcut help overlay (POL-8.5): rendered from the binding table, so the
 * listed hints can never drift from behavior. `?` opens it from anywhere. */
export function HelpOverlay() {
  return (
    <Show when={helpOpen()}>
      <OpenHelp />
    </Show>
  );
}

function OpenHelp() {
  let closeButton: HTMLButtonElement | undefined;
  let opener: Element | null = null;

  const close = () => {
    setHelpOpen(false);
    if (opener instanceof HTMLElement) opener.focus();
  };

  onMount(() => {
    opener = document.activeElement;
    closeButton?.focus();
  });

  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      event.preventDefault();
      close();
      return;
    }
    if (event.key !== "Tab") return;
    const root = (event.currentTarget as HTMLElement).querySelector(".dialog");
    const items = root
      ? Array.from(root.querySelectorAll<HTMLElement>("button")).filter(
          (el) => !el.hasAttribute("disabled"),
        )
      : [];
    if (!items.length) return;
    const first = items[0];
    const last = items[items.length - 1];
    if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    } else if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    }
  };

  return (
    <div
      class="modal-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) close();
      }}
    >
      <div
        class="dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Keyboard shortcuts"
        onKeyDown={onKeyDown}
      >
        <div class="modal-title">
          <h2>Keyboard shortcuts</h2>
          <button type="button" ref={closeButton} aria-label="Close shortcut help" onClick={close}>
            ×
          </button>
        </div>
        <div class="dialog-body">
          <ul class="shortcut-list">
            <For each={SHORTCUTS}>
              {(binding) => (
                <li>
                  <kbd>{binding.keys}</kbd>
                  <span>{binding.label}</span>
                </li>
              )}
            </For>
          </ul>
        </div>
      </div>
    </div>
  );
}
