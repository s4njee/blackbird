// @vitest-environment happy-dom
// Context menu keyboard map (POL-8.5): menu roles, initial focus, arrows,
// Home/End, and type-ahead across rendered items.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@solidjs/testing-library";
import type { Torrent } from "../../src/lib/types.js";

vi.mock("../../src/store/session.js", () => ({
  connected: () => true,
  patchTorrents: () => {},
  restoreTorrents: () => {},
  torrentList: () => [],
  visibleHashes: () => [],
  visibleRows: () => [],
}));

import { ContextMenu } from "../../src/components/ActionControls";

const row = { hash: "a", name: "alpha" } as Torrent;

afterEach(cleanup);

async function openMenu(onClose: () => void = () => {}) {
  render(() => (
    <ContextMenu target={{ x: 10, y: 10, hashes: ["a"], torrent: row }} onClose={onClose} />
  ));
  const menu = await screen.findByRole("menu");
  // Initial focus lands on a timer; wait for a real button, not body text.
  await waitFor(() => expect(document.activeElement?.tagName).toBe("BUTTON"));
  return menu as HTMLElement;
}

describe("ContextMenu keyboard", () => {
  it("exposes the menu role and focuses the first item", async () => {
    const menu = await openMenu();
    expect(menu.getAttribute("aria-label")).toBe("Torrent actions");
    await waitFor(() => expect(document.activeElement?.getAttribute("role")).toBe("menuitem"));
    expect(document.activeElement?.textContent).toContain("Start");
  });

  it("moves with arrows and jumps with Home/End", async () => {
    const menu = await openMenu();
    await waitFor(() => expect(document.activeElement?.textContent).toContain("Start"));
    fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement?.textContent).toContain("Pause");
    fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement?.textContent).toContain("Stop");
    fireEvent.keyDown(menu, { key: "ArrowUp" });
    expect(document.activeElement?.textContent).toContain("Pause");
    fireEvent.keyDown(menu, { key: "End" });
    expect(document.activeElement?.textContent).toContain("Remove + data");
    fireEvent.keyDown(menu, { key: "Home" });
    expect(document.activeElement?.textContent).toContain("Start");
  });

  it("type-ahead focuses the first matching label", async () => {
    const menu = await openMenu();
    await waitFor(() => expect(document.activeElement?.textContent).toContain("Start"));
    fireEvent.keyDown(menu, { key: "p" });
    expect(document.activeElement?.textContent).toContain("Pause");
  });

  it("marks submenu toggles with popup state and closes on Escape", async () => {
    let closed = 0;
    const menu = await openMenu(() => closed++);
    const priority = screen.getByRole("menuitem", { name: /Priority/ });
    expect(priority.getAttribute("aria-haspopup")).toBe("true");
    expect(priority.getAttribute("aria-expanded")).toBe("false");
    fireEvent.keyDown(menu, { key: "Escape" });
    expect(closed).toBe(1);
  });
});
