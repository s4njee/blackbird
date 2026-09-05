// @vitest-environment happy-dom
// Notice stack/centre tests (POL-8.3): severity rendering and live regions,
// action + dismiss wiring, overflow opener, centre list/clear/close.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@solidjs/testing-library";
import { NoticeCentre, NoticeStack } from "../../src/components/Notices";
import {
  centreOpen,
  notify,
  resetNotifications,
  setCentreOpen,
} from "../../src/store/notifications.js";

afterEach(() => {
  resetNotifications();
  cleanup();
  vi.useRealTimers();
});

describe("Notices", () => {
  it("renders severity variants with matching live regions", () => {
    render(() => <NoticeStack />);
    notify("ok", { kind: "success" });
    notify("note", { kind: "info" });
    const ok = screen.getByText("ok").closest(".notice")!;
    const note = screen.getByText("note").closest(".notice")!;
    expect(ok.className).toContain("notice-success");
    expect(ok.getAttribute("role")).toBe("status");
    expect(ok.getAttribute("aria-live")).toBe("polite");
    expect(note.className).toContain("notice-info");
  });

  it("announces errors assertively until dismissed", async () => {
    render(() => <NoticeStack />);
    notify("boom", { kind: "error" });
    const card = screen.getByText("boom").closest(".notice")!;
    expect(card.getAttribute("role")).toBe("alert");
    expect(card.getAttribute("aria-live")).toBe("assertive");
    fireEvent.click(screen.getByRole("button", { name: "Dismiss notification" }));
    await waitFor(() => expect(screen.queryByText("boom")).toBeNull());
  });

  it("runs actions and shows the coalesced count", () => {
    let ran = 0;
    render(() => <NoticeStack />);
    notify("label set", { action: { label: "Undo", run: () => ran++ } });
    notify("label set", { action: { label: "Undo", run: () => ran++ } });
    expect(screen.getByText("×2")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(ran).toBe(1);
    expect(screen.queryByText("label set")).toBeNull();
  });

  it("collapses past three into an overflow opener for the centre", async () => {
    render(() => (
      <>
        <NoticeStack />
        <NoticeCentre />
      </>
    ));
    for (let i = 0; i < 4; i++) notify(`m-${i}`);
    expect(screen.queryByText("m-3")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "+1 more" }));
    await waitFor(() => expect(centreOpen()).toBe(true));
    expect(screen.getByText("m-3")).toBeTruthy();
  });

  it("lists centre records with timestamps and clears them", async () => {
    render(() => <NoticeCentre />);
    notify("first", { kind: "warning" });
    notify("second", { kind: "error" });
    setCentreOpen(true);
    const dialog = await screen.findByRole("dialog");
    expect(dialog.getAttribute("aria-label")).toBe("Notification centre");
    const rows = dialog.querySelectorAll(".notice-row");
    expect(rows.length).toBe(2);
    expect(rows[0].querySelector("time")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.getByText("No notifications yet.")).toBeTruthy();
    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() => expect(centreOpen()).toBe(false));
  });

  it("traps focus inside the centre and restores the opener", async () => {
    const { container } = render(() => (
      <>
        <button type="button">opener</button>
        <NoticeCentre />
      </>
    ));
    const opener = container.querySelector("button")!;
    opener.focus();
    notify("hello");
    setCentreOpen(true);
    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(document.activeElement?.getAttribute("aria-label")).toBe("Close notifications"),
    );
    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() => expect(centreOpen()).toBe(false));
    expect(document.activeElement).toBe(opener);
  });
});
