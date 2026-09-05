// @vitest-environment happy-dom
// Dialog system tests (POL-8.2): primitive semantics, focus trap, focus
// restore, keyboard operation, prompt/form values, details cap, and the
// session "don't ask again" memory. No store mocks — the real dialog store
// drives DialogHost.
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@solidjs/testing-library";
import { DialogHost } from "../../src/components/Dialog";
import {
  cancelDialog,
  confirmDialog,
  dialogRequest,
  promptDialog,
  resetDialogSkips,
} from "../../src/store/dialog.js";

afterEach(() => {
  cancelDialog();
  resetDialogSkips();
  cleanup();
  hosted = false;
});

// One DialogHost per test: every open in a test shares it, so sequential
// dialogs never double-mount (stale unmounts would read as duplicates).
let hosted = false;
function host() {
  if (!hosted) {
    render(() => <DialogHost />);
    hosted = true;
  }
}

async function openConfirm(options?: Parameters<typeof confirmDialog>[0]) {
  host();
  const pending = confirmDialog({ title: "Remove", ...options });
  const dialog = await screen.findByRole("dialog");
  return { dialog, pending };
}

/** Waits until no dialog remains mounted (settled unmounts flush async). */
async function settledToNull() {
  await waitFor(() => expect(screen.queryAllByRole("dialog")).toHaveLength(0));
}

describe("Dialog", () => {
  it("renders a labelled modal confirm with custom action labels", async () => {
    const { dialog, pending } = await openConfirm({
      title: "Remove torrents",
      body: "Remove 2 torrents?",
      confirmLabel: "Remove",
      cancelLabel: "Keep",
    });
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    expect(dialog.getAttribute("aria-label")).toBe("Remove torrents");
    expect(dialog.textContent).toContain("Remove 2 torrents?");
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    await expect(pending).resolves.toBe(true);
    expect(dialogRequest()).toBeNull();
  });

  it("focuses the safe choice and traps Tab inside", async () => {
    const { dialog, pending } = await openConfirm({ title: "Danger", danger: true });
    const cancel = screen.getByRole("button", { name: "Cancel" });
    const confirm = screen.getByRole("button", { name: "Confirm" });
    await waitFor(() => expect(document.activeElement).toBe(cancel));
    // Tab on the last focusable wraps to the first.
    confirm.focus();
    fireEvent.keyDown(dialog, { key: "Tab" });
    expect(document.activeElement).toBe(cancel);
    // Shift+Tab on the first wraps to the last.
    fireEvent.keyDown(dialog, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(confirm);
    fireEvent.click(cancel);
    await expect(pending).resolves.toBe(false);
  });

  it("focuses confirm for non-danger dialogs", async () => {
    const { pending } = await openConfirm({ title: "Plain" });
    await waitFor(() =>
      expect(document.activeElement).toBe(screen.getByRole("button", { name: "Confirm" })),
    );
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await expect(pending).resolves.toBe(false);
  });

  it("closes on Escape and on backdrop press as cancel", async () => {
    const first = await openConfirm({ title: "One" });
    fireEvent.keyDown(first.dialog, { key: "Escape" });
    await expect(first.pending).resolves.toBe(false);
    await settledToNull();

    const second = await openConfirm({ title: "Two" });
    const backdrop = second.dialog.parentElement;
    expect(backdrop?.className).toContain("modal-backdrop");
    fireEvent.mouseDown(backdrop!);
    await expect(second.pending).resolves.toBe(false);
    expect(dialogRequest()).toBeNull();
  });

  it("restores focus to the opener on close", async () => {
    const { container } = render(() => (
      <>
        <button type="button">opener</button>
        <DialogHost />
      </>
    ));
    const opener = container.querySelector("button")!;
    opener.focus();
    expect(document.activeElement).toBe(opener);
    const pending = confirmDialog({ title: "Focus" });
    await screen.findByRole("dialog");
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    await expect(pending).resolves.toBe(false);
    expect(document.activeElement).toBe(opener);
  });

  it("prompts with an initial value and confirms on Enter", async () => {
    render(() => <DialogHost />);
    const pending = promptDialog({ title: "Rename", label: "Name", initial: "old" });
    const dialog = await screen.findByRole("dialog");
    const input = dialog.querySelector("input") as HTMLInputElement;
    expect(document.activeElement).toBe(input);
    expect(input.value).toBe("old");
    fireEvent.input(input, { target: { value: "new" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await expect(pending).resolves.toBe("new");
  });

  it("cancels prompts on Escape and disables confirm when empty is banned", async () => {
    render(() => <DialogHost />);
    const pending = promptDialog({ title: "Label", allowEmpty: false });
    await screen.findByRole("dialog");
    const confirm = screen.getByRole("button", { name: "Confirm" });
    expect((confirm as HTMLButtonElement).disabled).toBe(true);
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    await expect(pending).resolves.toBeNull();
  });

  it("collects form fields into a value record", async () => {
    render(() => <DialogHost />);
    const pending = promptDialog({
      title: "Add tracker",
      fields: [
        { key: "url", label: "URL" },
        { key: "tier", label: "Tier", initial: "0" },
      ],
      confirmLabel: "Add",
    });
    const dialog = await screen.findByRole("dialog");
    const inputs = Array.from(dialog.querySelectorAll("input"));
    expect(inputs.length).toBe(2);
    fireEvent.input(inputs[0], { target: { value: "https://t.example/announce" } });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    await expect(pending).resolves.toEqual({ url: "https://t.example/announce", tier: "0" });
  });

  it("caps detail lines and remembers don't-ask-again for the session", async () => {
    render(() => <DialogHost />);
    const details = Array.from({ length: 12 }, (_, i) => `/data/path-${i}`);
    const first = confirmDialog({ title: "Remove + data", details, skipKey: "rm" });
    const dialog = await screen.findByRole("dialog");
    expect(dialog.querySelectorAll(".dialog-details li").length).toBe(10);
    expect(dialog.textContent).toContain("…and 2 more");
    // Plain confirms offer the skip checkbox; danger ones never do.
    fireEvent.click(dialog.querySelector(".dialog-skip input")!);
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await expect(first).resolves.toBe(true);
    // Remembered: resolves without rendering.
    await expect(confirmDialog({ title: "Remove + data", skipKey: "rm" })).resolves.toBe(true);
    expect(dialogRequest()).toBeNull();
  });

  it("never offers skip for destructive confirms", async () => {
    render(() => <DialogHost />);
    confirmDialog({ title: "Danger", danger: true, skipKey: "nope" });
    const dialog = await screen.findByRole("dialog");
    expect(dialog.querySelector(".dialog-skip")).toBeNull();
    const first = confirmDialog({ title: "Danger", danger: true, skipKey: "nope" });
    void first;
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await settledToNull();
    // Not remembered: renders again.
    const again = confirmDialog({ title: "Danger", danger: true, skipKey: "nope" });
    void again;
    await screen.findByRole("dialog");
    expect(dialogRequest()).not.toBeNull();
  });
});
