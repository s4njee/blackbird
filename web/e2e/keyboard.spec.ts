import { expect, test } from "@playwright/test";
import { gotoConsole, rows, trackProblems } from "./helpers";

// Keyboard operation flows (POL-8.5). Same suite runs against the Compose
// appliance with E2E_BASE_URL set.

test("help overlay opens on ? and closes on Escape", async ({ page }) => {
  const problems = await trackProblems(page);
  await gotoConsole(page);
  await page.keyboard.press("?");
  const dialog = page.getByRole("dialog", { name: "Keyboard shortcuts" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("Add torrent");
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  expect(problems).toEqual([]);
});

test("A opens the add modal and F toggles the detail panel", async ({ page }) => {
  const problems = await trackProblems(page);
  await gotoConsole(page);
  await page.keyboard.press("a");
  await expect(page.locator(".add-modal")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator(".add-modal")).toHaveCount(0);
  await rows(page).first().click();
  await page.keyboard.press("f");
  await expect(page.locator(".detail-panel")).toContainText("Detail panel collapsed");
  await page.keyboard.press("f");
  await expect(page.locator(".detail-body")).toBeVisible();
  expect(problems).toEqual([]);
});

test("digits switch detail tabs with roving focus", async ({ page }) => {
  const problems = await trackProblems(page);
  await gotoConsole(page);
  await rows(page).first().click();
  await page.keyboard.press("2");
  await expect(page.locator(".peers-table")).toBeVisible();
  await expect(page.getByRole("tab", { name: "Peers" })).toHaveAttribute("aria-selected", "true");
  expect(problems).toEqual([]);
});

test("context menu arrows, Home/End, and Escape", async ({ page }) => {
  const problems = await trackProblems(page);
  await gotoConsole(page);
  await rows(page).first().click({ button: "right" });
  const menu = page.getByRole("menu", { name: "Torrent actions" });
  await expect(menu).toBeVisible();
  await expect(page.getByRole("menuitem", { name: "Start", exact: true })).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(page.getByRole("menuitem", { name: "Pause", exact: true })).toBeFocused();
  await page.keyboard.press("End");
  await expect(page.getByRole("menuitem", { name: "Remove + data" })).toBeFocused();
  await page.keyboard.press("Home");
  await expect(page.getByRole("menuitem", { name: "Start", exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
  expect(problems).toEqual([]);
});

test("detail tablist arrows move between tabs", async ({ page }) => {
  const problems = await trackProblems(page);
  await gotoConsole(page);
  await rows(page).first().click();
  await page.getByRole("tab", { name: "Files" }).click();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: "Peers" })).toHaveAttribute("aria-selected", "true");
  await expect(page.locator(".peers-table")).toBeVisible();
  expect(problems).toEqual([]);
});
