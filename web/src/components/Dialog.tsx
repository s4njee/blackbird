import { For, Show, createEffect, createSignal, onCleanup, onMount, type JSX } from "solid-js";
import {
  cancelDialog,
  dialogRequest,
  type ConfirmRequest,
  type DialogRequest,
  type PromptRequest,
} from "../store/dialog";

/** Detail lines shown before the "+ N more" cap (data-removal paths). */
const MAX_DETAILS = 10;

function focusables(root: HTMLElement): HTMLElement[] {
  const found = Array.from(root.querySelectorAll<HTMLElement>("button, input, [tabindex]")).filter(
    (el) => !el.hasAttribute("disabled") && el.tabIndex >= 0,
  );
  // querySelectorAll is document order; the skip checkbox precedes the
  // actions row, which matches the visual tab order.
  return found;
}

/** Modal shell: focus-trapped, labelled, Escape/backdrop to cancel, focus
 * restored to the opener on close. */
function Shell(props: { request: DialogRequest; label: string; children: JSX.Element }) {
  let root: HTMLDivElement | undefined;
  let opener: Element | null = null;

  onMount(() => {
    opener = document.activeElement;
  });

  // Initial focus: first input for prompts/forms, the safe choice for
  // confirms (cancel when danger, confirm otherwise).
  createEffect(() => {
    const el = root;
    if (!el) return;
    const req = props.request;
    const selector =
      req.kind === "prompt" ? ".dialog-input" : req.danger ? ".dialog-cancel" : ".dialog-confirm";
    el.querySelector<HTMLElement>(selector)?.focus();
  });

  onCleanup(() => {
    if (opener instanceof HTMLElement) opener.focus();
  });

  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      event.preventDefault();
      cancelDialog();
      return;
    }
    if (event.key !== "Tab" || !root) return;
    const items = focusables(root);
    if (!items.length) {
      event.preventDefault();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    } else if (event.shiftKey && active === first) {
      event.preventDefault();
      last.focus();
    }
  };

  return (
    <div
      class="modal-backdrop"
      onMouseDown={(event) => event.target === event.currentTarget && cancelDialog()}
    >
      <div
        ref={root}
        class="dialog"
        role="dialog"
        aria-modal="true"
        aria-label={props.label}
        onKeyDown={onKeyDown}
      >
        {props.children}
      </div>
    </div>
  );
}

function ConfirmView(props: { request: ConfirmRequest }) {
  const [dontAsk, setDontAsk] = createSignal(false);
  const req = () => props.request;
  const shown = () => (req().details ?? []).slice(0, MAX_DETAILS);
  const hidden = () => Math.max(0, (req().details ?? []).length - shown().length);
  return (
    <Shell request={req()} label={req().title}>
      <div class="modal-title">
        <h2>{req().title}</h2>
      </div>
      <div class="dialog-body">
        <Show when={req().body}>
          <p>{req().body}</p>
        </Show>
        <Show when={shown().length}>
          <ul class="dialog-details">
            <For each={shown()}>{(line) => <li title={line}>{line}</li>}</For>
          </ul>
          <Show when={hidden() > 0}>
            <p class="dialog-more">…and {hidden()} more</p>
          </Show>
        </Show>
        <Show when={req().skipKey && !req().danger}>
          <label class="dialog-skip">
            <input
              type="checkbox"
              checked={dontAsk()}
              onChange={(event) => setDontAsk(event.currentTarget.checked)}
            />
            <span>Don’t ask again for this session</span>
          </label>
        </Show>
      </div>
      <div class="dialog-actions">
        <button type="button" class="dialog-cancel" onClick={() => req().settle(false, false)}>
          {req().cancelLabel ?? "Cancel"}
        </button>
        <button
          type="button"
          class="dialog-confirm"
          classList={{ danger: req().danger }}
          onClick={() => req().settle(true, dontAsk())}
        >
          {req().confirmLabel ?? "Confirm"}
        </button>
      </div>
    </Shell>
  );
}

function PromptView(props: { request: PromptRequest }) {
  const req = () => props.request;
  const fields = () =>
    req().fields ?? [{ key: "value", label: req().label ?? "", initial: req().initial ?? "" }];
  const [values, setValues] = createSignal<Record<string, string>>(
    Object.fromEntries(fields().map((f): [string, string] => [f.key, f.initial ?? ""])),
  );
  const empty = () => Object.values(values()).every((v) => !v.trim());
  const blocked = () => req().allowEmpty === false && empty();
  const submit = () => {
    if (blocked()) return;
    const v = values();
    req().settle(req().fields ? v : (v["value"] ?? ""));
  };
  return (
    <Shell request={req()} label={req().title}>
      <div class="modal-title">
        <h2>{req().title}</h2>
      </div>
      <div class="dialog-body">
        <For each={fields()}>
          {(field, index) => (
            <label class="dialog-field">
              <Show when={field.label}>
                <span>{field.label}</span>
              </Show>
              <input
                type="text"
                class="dialog-input"
                value={values()[field.key] ?? ""}
                placeholder={index() === 0 ? (req().placeholder ?? "") : ""}
                aria-label={field.label || req().title}
                onInput={(event) =>
                  setValues((prev) => ({ ...prev, [field.key]: event.currentTarget.value }))
                }
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    event.preventDefault();
                    submit();
                  }
                }}
              />
            </label>
          )}
        </For>
      </div>
      <div class="dialog-actions">
        <button type="button" class="dialog-cancel" onClick={() => req().settle(null)}>
          {req().cancelLabel ?? "Cancel"}
        </button>
        <button type="button" class="dialog-confirm" disabled={blocked()} onClick={submit}>
          {req().confirmLabel ?? "Confirm"}
        </button>
      </div>
    </Shell>
  );
}

/** Top-level host: renders the queued dialog, if any. Mount once in App. */
export function DialogHost() {
  return (
    <Show when={dialogRequest()}>
      {(req) => {
        const current = req();
        return current.kind === "confirm" ? (
          <ConfirmView request={current} />
        ) : (
          <PromptView request={current} />
        );
      }}
    </Show>
  );
}
