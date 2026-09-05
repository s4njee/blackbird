// In-app dialog system (POL-8.2): one promise-based primitive backing
// confirm, prompt, and form dialogs, replacing every window.confirm /
// window.prompt call site. Rendering lives in components/Dialog.tsx
// (DialogHost); this module owns the queue state and the session-scoped
// "don't ask again" memory.

import { createSignal } from "solid-js";

export type ConfirmRequest = {
  kind: "confirm";
  title: string;
  body?: string;
  /** Extra detail lines (e.g. data-removal paths), capped in the view. */
  details?: string[];
  confirmLabel?: string;
  cancelLabel?: string;
  /** Destructive confirms get danger styling, focus the safe button, and
   * never participate in "don't ask again". */
  danger?: boolean;
  /** Session key for "don't ask again for this session" (non-danger only).
   * While remembered, calls resolve true without rendering. */
  skipKey?: string;
  settle: (value: boolean, dontAsk: boolean) => void;
};

export type PromptField = {
  key: string;
  label: string;
  initial?: string;
  placeholder?: string;
};

export type PromptRequest = {
  kind: "prompt";
  title: string;
  /** Single-field prompt shape. */
  label?: string;
  initial?: string;
  placeholder?: string;
  /** Multi-field form shape; when present the result is a value record. */
  fields?: PromptField[];
  confirmLabel?: string;
  cancelLabel?: string;
  /** When false, an empty (or all-empty) value keeps confirm disabled. */
  allowEmpty?: boolean;
  settle: (value: string | Record<string, string> | null) => void;
};

export type DialogRequest = ConfirmRequest | PromptRequest;

const [current, setCurrent] = createSignal<DialogRequest | null>(null);

/** The dialog on screen, if any. Rendered by DialogHost. */
export { current as dialogRequest };

/** Session-scoped "don't ask again" memory (in-memory only). */
const skipped = new Set<string>();

/** Clears remembered skips (tests). */
export function resetDialogSkips() {
  skipped.clear();
}

export function confirmDialog(options: {
  title: string;
  body?: string;
  details?: string[];
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
  skipKey?: string;
}): Promise<boolean> {
  if (options.skipKey && !options.danger && skipped.has(options.skipKey)) {
    return Promise.resolve(true);
  }
  return new Promise((resolve) => {
    setCurrent({
      kind: "confirm",
      title: options.title,
      body: options.body,
      details: options.details,
      confirmLabel: options.confirmLabel,
      cancelLabel: options.cancelLabel,
      danger: options.danger,
      skipKey: options.skipKey,
      settle: (value, dontAsk) => {
        if (value && dontAsk && options.skipKey && !options.danger) {
          skipped.add(options.skipKey);
        }
        setCurrent(null);
        resolve(value);
      },
    });
  });
}

export function promptDialog(options: {
  title: string;
  label?: string;
  initial?: string;
  placeholder?: string;
  fields?: PromptField[];
  confirmLabel?: string;
  cancelLabel?: string;
  allowEmpty?: boolean;
}): Promise<string | Record<string, string> | null> {
  return new Promise((resolve) => {
    setCurrent({
      kind: "prompt",
      title: options.title,
      label: options.label,
      initial: options.initial,
      placeholder: options.placeholder,
      fields: options.fields,
      confirmLabel: options.confirmLabel,
      cancelLabel: options.cancelLabel,
      allowEmpty: options.allowEmpty,
      settle: (value) => {
        setCurrent(null);
        resolve(value);
      },
    });
  });
}

/** Dismisses the current dialog as cancelled (Escape, backdrop). */
export function cancelDialog() {
  const req = current();
  if (!req) return;
  if (req.kind === "confirm") req.settle(false, false);
  else req.settle(null);
}
