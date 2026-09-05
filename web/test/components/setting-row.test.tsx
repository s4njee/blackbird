// @vitest-environment happy-dom
// SettingRow apply badges (POL-8.4): restart/reconnect markers render next
// to the label; live fields show none.
import { describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@solidjs/testing-library";

vi.mock("../../src/store/session.js", () => ({
  connected: () => true,
  globalStats: () => null,
  torrentList: () => [],
}));

import { SettingRow } from "../../src/components/SettingsPanel";

describe("SettingRow", () => {
  it("shows a restart badge when the field needs it", () => {
    render(() => (
      <SettingRow label="Endpoint" hint="rtorrent.scgi" needs="restart">
        <span>ctl</span>
      </SettingRow>
    ));
    expect(screen.getByText("restart required").className).toContain("needs-restart");
    cleanup();
  });

  it("shows a reconnect badge when the field needs it", () => {
    render(() => (
      <SettingRow label="Endpoint" hint="proxy" needs="reconnect">
        <span>ctl</span>
      </SettingRow>
    ));
    expect(screen.getByText("reconnect required")).toBeTruthy();
    cleanup();
  });

  it("shows no badge for live fields", () => {
    const { container } = render(() => (
      <SettingRow label="Accent color" hint="ui.accent">
        <span>ctl</span>
      </SettingRow>
    ));
    expect(container.querySelector(".needs-badge")).toBeNull();
    cleanup();
  });
});
