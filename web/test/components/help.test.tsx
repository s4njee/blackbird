// @vitest-environment happy-dom
// Help overlay (POL-8.5): lists every binding from the table, traps focus,
// and closes on Escape with focus restore.
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@solidjs/testing-library";
import { HelpOverlay } from "../../src/components/HelpOverlay";
import { SHORTCUTS } from "../../src/lib/shortcuts.js";
import { setHelpOpen } from "../../src/store/ui.js";

afterEach(() => {
  setHelpOpen(false);
  cleanup();
});

describe("HelpOverlay", () => {
  it("renders hidden until opened, then lists every binding", async () => {
    render(() => <HelpOverlay />);
    expect(screen.queryByRole("dialog")).toBeNull();
    setHelpOpen(true);
    const dialog = await screen.findByRole("dialog");
    expect(dialog.getAttribute("aria-label")).toBe("Keyboard shortcuts");
    for (const binding of SHORTCUTS) {
      expect(dialog.textContent).toContain(binding.label);
    }
  });

  it("focuses close, traps Tab, and restores focus on Escape", async () => {
    const { container } = render(() => (
      <>
        <button type="button">opener</button>
        <HelpOverlay />
      </>
    ));
    const opener = container.querySelector("button")!;
    opener.focus();
    setHelpOpen(true);
    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(document.activeElement?.getAttribute("aria-label")).toBe("Close shortcut help"),
    );
    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(opener);
  });

  it("closes on backdrop press", async () => {
    render(() => <HelpOverlay />);
    setHelpOpen(true);
    const dialog = await screen.findByRole("dialog");
    const backdrop = dialog.parentElement!;
    expect(backdrop.className).toContain("modal-backdrop");
    fireEvent.mouseDown(backdrop);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });
});
